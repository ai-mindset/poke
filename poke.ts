#!/usr/bin/env deno run --allow-env --allow-net --allow-read --allow-run

interface GitHubIssue {
  id: number;
  number: number;
  title: string;
  html_url: string;
  repository_url: string;
  state: string;
  pull_request?: { url: string };
  assignees: { login: string }[];
  requested_reviewers?: { login: string }[];
  repository?: { full_name: string };
}

let GITHUB_TOKEN = Deno.env.get("GITHUB_TOKEN");
let WORK_ORGS: string[] = [];
let WORK_TEAMS: string[] = [];
let GITHUB_USERNAME = "";

// Load environment variables
if (!GITHUB_TOKEN) {
  const projectName = "poke";
  const homeDir = Deno.env.get("HOME") || Deno.env.get("USERPROFILE") || "";
  const configPath = `${homeDir}/.config/${projectName}/.env`;

  try {
    const envFile = await Deno.readTextFile(configPath);

    const tokenMatch = envFile.match(/^GITHUB_TOKEN=(.+)$/m);
    GITHUB_TOKEN = tokenMatch?.[1]?.replace(/[""]/g, "");

    const orgsMatch = envFile.match(/^WORK_ORGS=(.+)$/m);
    if (orgsMatch?.[1]) {
      WORK_ORGS = orgsMatch[1].replace(/[""]/g, "").split(",").map((o) =>
        o.trim()
      );
    }

    const teamsMatch = envFile.match(/^WORK_TEAMS=(.+)$/m);
    if (teamsMatch?.[1]) {
      WORK_TEAMS = teamsMatch[1].replace(/[""]/g, "").split(",").map((t) =>
        t.trim()
      );
    }

    const usernameMatch = envFile.match(/^GITHUB_USERNAME=(.+)$/m);
    if (usernameMatch?.[1]) {
      GITHUB_USERNAME = usernameMatch[1].replace(/[""]/g, "").trim();
    }
  } catch {
    // File doesn't exist
  }
}

if (!GITHUB_TOKEN) {
  console.error(
    "GitHub token required. Create ~/.config/poke/.env with GITHUB_TOKEN=your_token",
  );
  Deno.exit(1);
}

async function searchGitHub(query: string): Promise<GitHubIssue[]> {
  const headers = {
    "Authorization": `Bearer ${GITHUB_TOKEN}`,
    "Accept": "application/vnd.github+json",
    "X-GitHub-Api-Version": "2022-11-28",
  };

  // Break down the query into safe parts and encode each separately
  const safeQuery = query.replace(/\(/g, "%28").replace(/\)/g, "%29");
  const encodedQuery = encodeURIComponent(safeQuery);
  const url =
    `https://api.github.com/search/issues?q=${encodedQuery}&per_page=100`;

  // Log all queries only when the debug flag is set
  if (Deno.args.includes("--debug")) {
    console.log(`Searching: ${query}`);
  }

  const response = await fetch(url, { headers });

  if (!response.ok) {
    throw new Error(
      `GitHub API error: ${response.status} - ${await response.text()}`,
    );
  }

  const data = await response.json();
  const items: GitHubIssue[] = data.items || [];

  // Add repository full_name to each item
  for (const item of items) {
    const repoUrl = item.repository_url;
    item.repository = {
      full_name: repoUrl.replace("https://api.github.com/repos/", ""),
    };
  }

  return items;
}

async function fetchAllViews(org?: string): Promise<{
  todo: GitHubIssue[];
  done: GitHubIssue[];
  toReview: GitHubIssue[];
  reviewed: GitHubIssue[];
  mentioned: GitHubIssue[];
}> {
  // Get organization name
  const orgName = org || (WORK_ORGS.length > 0 ? WORK_ORGS[0] : "");
  if (!orgName) {
    throw new Error("No organization specified and no default in WORK_ORGS");
  }

  console.log(`Using organization: ${orgName}`);

  const todoQuery =
    `is:issue state:open archived:false assignee:@me sort:updated-desc org:${orgName}`;
  const doneQuery =
    `is:issue state:closed assignee:@me sort:updated-desc org:${orgName}`;

  // Separate queries to avoid syntax issues
  const personalReviewQuery = `is:pr review-requested:@me org:${orgName}`;
  const prReviewedQuery = `is:pr reviewed-by:@me org:${orgName}`;
  const prMentionsQuery = `is:pr is:open mentions:@me org:${orgName}`;

  // Fetch issues and personal review PRs first
  const [todo, done, personalReviews, reviewed, mentioned] = await Promise.all([
    searchGitHub(todoQuery),
    searchGitHub(doneQuery),
    searchGitHub(personalReviewQuery),
    searchGitHub(prReviewedQuery),
    searchGitHub(prMentionsQuery),
  ]);

  // Now fetch team review PRs separately for each team
  let teamReviews: GitHubIssue[] = [];

  if (WORK_TEAMS.length > 0) {
    // Make a separate search for each team to avoid query syntax issues
    for (const team of WORK_TEAMS) {
      try {
        const teamQuery =
          `is:pr team-review-requested:${orgName}/${team} org:${orgName}`;
        const teamResults = await searchGitHub(teamQuery);
        teamReviews = teamReviews.concat(teamResults);
      } catch (error) {
        console.error(
          `Error fetching team ${team} reviews:`,
          (error as Error).message,
        );
      }
    }
  }

  // Combine personal reviews and team reviews, removing duplicates
  const seenIds = new Set<number>();
  const allReviews: GitHubIssue[] = [];

  // Add personal reviews first (higher priority)
  for (const pr of personalReviews) {
    allReviews.push(pr);
    seenIds.add(pr.id);
  }

  // Add team reviews that weren't already in personal reviews
  for (const pr of teamReviews) {
    if (!seenIds.has(pr.id)) {
      allReviews.push(pr);
      seenIds.add(pr.id);
    }
  }

  return {
    todo,
    done,
    toReview: allReviews,
    reviewed,
    mentioned,
  };
}

function displayItem(item: GitHubIssue, category: string): void {
  const isPR = !!item.pull_request;
  let icon = "📝";

  // Different icons for different categories
  if (category === "toReview") {
    icon = "✅"; // PRs to review
  } else if (category === "reviewed") {
    icon = "👍"; // PRs reviewed
  } else if (category === "done") {
    icon = "✓"; // Completed issues
  } else if (isPR) {
    icon = "🔄"; // Any other PR
  }

  console.log(`${icon} [${item.repository?.full_name}] ${item.title}`);
  console.log(`   #${item.number} • ${category} • ${item.html_url}\n`);
}

// Send desktop notification
async function sendNotification(title: string, body: string): Promise<void> {
  try {
    const cmd = new Deno.Command("notify-send", {
      args: [title, body],
    });
    await cmd.output();
  } catch (error) {
    console.error(
      "Failed to send desktop notification:",
      (error as Error).message,
    );
  }
}

// Main function
if (import.meta.main) {
  try {
    // Check token scopes
    console.log("Checking GitHub token permissions...");
    const userResponse = await fetch("https://api.github.com/user", {
      headers: {
        "Authorization": `Bearer ${GITHUB_TOKEN}`,
        "Accept": "application/vnd.github+json",
      },
    });

    if (userResponse.ok) {
      const scopes = userResponse.headers.get("X-OAuth-Scopes");
      console.log(`Token scopes: ${scopes || "unknown"}`);

      if (!scopes || !scopes.includes("repo")) {
        console.warn("WARNING: Your token may not have 'repo' scope!");
        console.warn(
          "Create a new token with 'repo' scope at: https://github.com/settings/tokens",
        );
      }
    }

    // Get org filter from command line
    const orgFilter = Deno.args.length > 0 && !Deno.args[0].startsWith("--")
      ? Deno.args[0]
      : undefined;

    const notificationCountArg = Deno.args.find((arg) =>
      arg.startsWith("--notifications=")
    );
    const notificationCount = notificationCountArg
      ? parseInt(notificationCountArg.split("=")[1], 10)
      : 10;

    // Fetch all views
    const views = await fetchAllViews(orgFilter);
    const totalItems = views.todo.length + views.toReview.length +
      views.done.length + views.reviewed.length;

    if (totalItems === 0) {
      console.log(`No items found${orgFilter ? ` for ${orgFilter}` : ""}`);
    } else {
      // Display PRs to review first (highest priority)
      if (views.toReview.length > 0) {
        console.log(
          `\n🔥 ${views.toReview.length} Pull Requests need your review:\n`,
        );
        views.toReview.forEach((item) => displayItem(item, "toReview"));
      }

      // Display assigned tasks next
      if (views.todo.length > 0) {
        console.log(`\n📋 ${views.todo.length} Issues assigned to you:\n`);
        views.todo.forEach((item) => displayItem(item, "todo"));
      }

      // Display recently completed items if any
      if (views.done.length > 0) {
        console.log(`\n✓ ${views.done.length} Issues recently completed:\n`);
        views.done.slice(0, 5).forEach((item) => displayItem(item, "done"));
      }

      // Display recently reviewed PRs
      if (views.reviewed.length > 0) {
        console.log(
          `\n👍 ${views.reviewed.length} Pull Requests you've reviewed:\n`,
        );
        views.reviewed.slice(0, 5).forEach((item) =>
          displayItem(item, "reviewed")
        );
      }

      // Display PRs where the user is mentioned
      if (views.mentioned.length > 0) {
        console.log(
          `\n💬 ${views.mentioned.length} Pull Requests where you're mentioned:\n`,
        );
        views.mentioned.forEach((item) => displayItem(item, "mentioned"));
      }

      // Prepare desktop notification focusing on highest priority items
      let notificationItems: GitHubIssue[] = [];

      // PRs to review are highest priority - use half of available slots
      const reviewSlots = Math.ceil(notificationCount / 2);
      notificationItems = notificationItems.concat(
        views.toReview.slice(0, reviewSlots),
      );

      // Add todo items with remaining slots
      if (notificationItems.length < notificationCount) {
        notificationItems = notificationItems.concat(
          views.todo.slice(0, notificationCount - notificationItems.length),
        );
      }

      // Add reviewed items if we still have space
      if (notificationItems.length < notificationCount) {
        notificationItems = notificationItems.concat(
          views.reviewed.slice(0, notificationCount - notificationItems.length),
        );
      }

      if (notificationItems.length > 0) {
        const title = `GitHub: ${views.toReview.length} PR${
          views.toReview.length !== 1 ? "s" : ""
        } to review, ${views.todo.length} issue${
          views.todo.length !== 1 ? "s" : ""
        } assigned`;

        const body = notificationItems.map((item) => {
          const isPR = !!item.pull_request;
          const icon = isPR ? "✅" : "📝";
          const repo = item.repository?.full_name || "";
          let title = item.title;
          if (title.length > 60) {
            title = title.substring(0, 57) + "...";
          }

          return `${icon} [${repo}] ${title}`;
        }).join("\n");

        await sendNotification(title, body);
      }
    }
  } catch (error) {
    console.error("Error:", (error as Error).message);
    Deno.exit(1);
  }
}
