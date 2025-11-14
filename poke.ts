#!/usr/bin/env deno run --allow-env --allow-net --allow-read --allow-run

import { join } from "https://deno.land/std/path/mod.ts";

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
  labels?: { name: string }[];
}

interface TodoItem {
  status: "backlog" | "in-progress" | "blocked" | "review";
  notes: string;
  lastUpdated: string;
  title?: string; // Store issue title for context
  url?: string; // Store issue URL for quick access
}

interface TodoStorage {
  issues: Record<string, TodoItem>;
}

let GITHUB_TOKEN = Deno.env.get("GITHUB_TOKEN");
let WORK_ORGS: string[] = [];
let WORK_TEAMS: string[] = [];

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
      WORK_ORGS = orgsMatch[1].replace(/[""]/g, "").split(",").map((o) => o.trim());
    }

    const teamsMatch = envFile.match(/^WORK_TEAMS=(.+)$/m);
    if (teamsMatch?.[1]) {
      WORK_TEAMS = teamsMatch[1].replace(/[""]/g, "").split(",").map((t) => t.trim());
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
  const url = `https://api.github.com/search/issues?q=${encodedQuery}&per_page=100`;

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

  const todoQuery = `is:issue state:open archived:false assignee:@me sort:updated-desc org:${orgName}`;
  const doneQuery = `is:issue state:closed assignee:@me sort:updated-desc org:${orgName}`;

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
        const teamQuery = `is:pr team-review-requested:${orgName}/${team} org:${orgName}`;
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

// Get issue key from GitHubIssue
function getIssueKey(item: GitHubIssue): string {
  return `${item.repository?.full_name}#${item.number}`;
}

// Load todo data from file
async function loadTodoData(): Promise<TodoStorage> {
  const homeDir = Deno.env.get("HOME") || Deno.env.get("USERPROFILE") || "";
  const todoPath = join(homeDir, ".poke", "todo.json");
  try {
    const todoFile = await Deno.readTextFile(todoPath);
    const data = JSON.parse(todoFile);

    // Ensure the issues object exists
    if (!data.issues) {
      data.issues = {};
    }

    return data;
  } catch {
    // File doesn't exist or can't be read
    return { issues: {} };
  }
}

// Save todo data to file
async function saveTodoData(data: TodoStorage): Promise<void> {
  const homeDir = Deno.env.get("HOME") || Deno.env.get("USERPROFILE") || "";
  const todoDir = join(homeDir, ".poke");
  const todoPath = join(todoDir, "todo.json");

  try {
    // Ensure directory exists
    await Deno.mkdir(todoDir, { recursive: true });

    // Write the file
    await Deno.writeTextFile(todoPath, JSON.stringify(data, null, 2));
    console.log(`Saved to ${todoPath}`);
  } catch (error) {
    console.error(`Error saving todo data: ${(error as Error).message}`);
    console.log(`Tried to save to path: ${todoPath}`);
  }
}

// Get status from GitHub labels
function getStatusFromLabels(labels: { name: string }[] = []): string {
  for (const label of labels) {
    if (label.name.startsWith("status:")) {
      return label.name.replace("status:", "");
    }
  }
  return "backlog";
}

// Update issue status via GitHub labels
async function updateIssueStatus(repo: string, issueNumber: number, status: string): Promise<boolean> {
  // Extract owner and repo name
  const [owner, repoName] = repo.split("/");
  if (!owner || !repoName) {
    console.error("Invalid repository format. Use 'owner/repo'");
    return false;
  }

  // Define the label to add
  const statusLabel = `status:${status}`;

  // Get current labels to handle existing status labels
  const currentLabelsUrl = `https://api.github.com/repos/${owner}/${repoName}/issues/${issueNumber}/labels`;
  const currentLabelsResponse = await fetch(currentLabelsUrl, {
    headers: {
      "Authorization": `Bearer ${GITHUB_TOKEN}`,
      "Accept": "application/vnd.github+json",
      "X-GitHub-Api-Version": "2022-11-28",
    },
  });

  if (!currentLabelsResponse.ok) {
    console.error(`Error getting labels: ${await currentLabelsResponse.text()}`);
    return false;
  }

  const currentLabels = await currentLabelsResponse.json();

  // Remove existing status labels and keep other labels
  const labelsToKeep = currentLabels
    .map((label: any) => label.name)
    .filter((name: string) => !name.startsWith("status:"));

  // Add the new status label
  labelsToKeep.push(statusLabel);

  // Update labels on the issue (replace all labels)
  const updateUrl = `https://api.github.com/repos/${owner}/${repoName}/issues/${issueNumber}/labels`;
  const updateResponse = await fetch(updateUrl, {
    method: "PUT",
    headers: {
      "Authorization": `Bearer ${GITHUB_TOKEN}`,
      "Accept": "application/vnd.github+json",
      "X-GitHub-Api-Version": "2022-11-28",
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ labels: labelsToKeep }),
  });

  if (!updateResponse.ok) {
    console.error(`Error updating labels: ${await updateResponse.text()}`);
    return false;
  }

  return true;
}

function displayItem(item: GitHubIssue, category: string, todoData?: TodoStorage): void {
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

  // If in todo mode, use status-based icons
  if (todoData && category === "todo") {
    const issueKey = getIssueKey(item);
    const todoItem = todoData.issues[issueKey];

    if (todoItem) {
      // Status-based icons
      switch (todoItem.status) {
        case "in-progress":
          icon = "🟨"; // In progress
          break;
        case "blocked":
          icon = "🟥"; // Blocked
          break;
        case "review":
          icon = "🟦"; // Ready for review
          break;
        default:
          icon = "⬜"; // Backlog (default)
      }
    }
  }

  console.log(`${icon} [${item.repository?.full_name}] ${item.title}`);

  // Standard display
  if (!todoData) {
    console.log(`   #${item.number} • ${category} • ${item.html_url}\n`);
    return;
  }

  // Enhanced display with todo info
  const issueKey = getIssueKey(item);
  const todoItem = todoData.issues[issueKey];

  if (todoItem) {
    // Use the URL from todoItem if available (fallback to item.html_url)
    const url = todoItem.url || item.html_url;
    console.log(`   #${item.number} • ${todoItem.status} • ${url}`);

    // Show title if available in todo storage (and different from item title)
    if (todoItem.title && todoItem.title !== item.title) {
      console.log(`   💡 Title: ${todoItem.title}`);
    }

    // Show notes if available
    if (todoItem.notes) {
      console.log(`   📝 Notes:\n     ${todoItem.notes.replace(/\n/g, '\n     ')}`);
    }

    console.log("");
  } else {
    console.log(`   #${item.number} • backlog • ${item.html_url}\n`);
  }
}

// Display a stored todo item (used in standalone mode)
function displayStoredItem(issueKey: string, item: TodoItem): void {
  // Get status icon
  let icon = "⬜"; // Default: backlog
  switch (item.status) {
    case "in-progress":
      icon = "🟨";
      break;
    case "blocked":
      icon = "🟥";
      break;
    case "review":
      icon = "🟦";
      break;
  }

  // Parse the repo and issue number from the key
  const [repo, issueNum] = issueKey.split("#");

  // Display the title if available, otherwise just the issue key
  if (item.title) {
    console.log(`${icon} [${repo}] ${item.title}`);
  } else {
    console.log(`${icon} ${issueKey}`);
  }

  // Display URL if available
  if (item.url) {
    console.log(`   #${issueNum} • ${item.status} • ${item.url}`);
  } else {
    console.log(`   #${issueNum} • ${item.status}`);
  }

  // Display notes if available
  if (item.notes) {
    console.log(`   📝 Notes:\n     ${item.notes.replace(/\n/g, '\n     ')}`);
  }

  console.log("");
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
    // Check if help is requested
    if (Deno.args.includes("--help") || Deno.args.includes("-h")) {
      console.log(`
Poke - GitHub notification manager with to-do list functionality

Standard usage:
  poke                                  - Show all notifications
  poke org-name                         - Filter for specific organization
  poke --debug                          - Show debug information
  poke --gui_notifications=N            - Control notification count
  poke -n=N                             - Short form for notification count

To-do list functionality:
  poke --todo                           - Show all to-do items (assigned + manually added)
  poke --update=org/repo#123 --status=S - Update issue status
  poke --update=org/repo#123 --note="..." - Add or update note for issue
  poke --update=org/repo#123 --show     - Update and show your to-do list

Status values:
  backlog, in-progress, blocked, review

Examples:
  poke --todo                           - View your to-do list
  poke --update=myorg/repo#42 --status=in-progress
  poke --update=myorg/repo#42 --note="Working on authentication fix"
  poke --update=myorg/repo#42 --status=blocked --note="Waiting for API access"
      `);
      Deno.exit(0);
    }

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
    const orgFilter = Deno.args.length > 0 && !Deno.args[0].startsWith("--") ? Deno.args[0] : undefined;

    const notificationCountArg = Deno.args.find((arg) => arg.startsWith("--gui_notifications=") || arg.startsWith("-n="));
    const notificationCount = notificationCountArg ? parseInt(notificationCountArg.split("=")[1], 10) : 10;

    // Check for todo mode - shows all todo items
    let todoMode = Deno.args.includes("--todo");

    // Check for issue update
    const updateArg = Deno.args.find((arg) => arg.startsWith("--update="));
    const issueKey = updateArg ? updateArg.split("=")[1] : null;

    // Get status if provided
    const statusArg = Deno.args.find((arg) => arg.startsWith("--status="));
    const rawStatus = statusArg ? statusArg.split("=")[1] : null;

    // Validate status to match allowed types
    const status = rawStatus && ["backlog", "in-progress", "blocked", "review"].includes(rawStatus)
      ? rawStatus as "backlog" | "in-progress" | "blocked" | "review"
      : null;

    // Get note if provided (handle complex notes with = characters)
    const noteArg = Deno.args.find((arg) => arg.startsWith("--note="));
    const note = noteArg ? noteArg.substring(7) : null;

    // If updating an issue
    if (issueKey && (status || note !== null)) {
      if (issueKey.includes("#")) {
        const [repo, issueNumberStr] = issueKey.split("#");
        const issueNumber = parseInt(issueNumberStr);

        if (isNaN(issueNumber)) {
          console.error("Invalid issue number in key. Use 'owner/repo#123'");
          Deno.exit(1);
        }

        // Update GitHub labels if status provided
        if (status) {
          console.log(`Updating GitHub status for ${issueKey} to ${status}...`);
          const updated = await updateIssueStatus(repo, issueNumber, status);
          if (!updated) {
            console.error(`Failed to update GitHub status for ${issueKey}`);
          } else {
            console.log(`Updated GitHub status for ${issueKey} to ${status}`);
          }
        }

        // Fetch issue details to get title and URL
        console.log(`Fetching issue details for ${issueKey}...`);
        try {
          let issueUrl;

          // Handle standard repo format (owner/repo) vs single word format
          if (repo.includes("/")) {
            issueUrl = `https://api.github.com/repos/${repo}/issues/${issueNumber}`;
          } else {
            // For non-standard repo formats like "nhp_products", we can't reliably fetch
            // Instead, just use local storage without fetching details
            throw new Error("Non-standard repository format - using local storage only");
          }

          const issueResponse = await fetch(issueUrl, {
            headers: {
              "Authorization": `Bearer ${GITHUB_TOKEN}`,
              "Accept": "application/vnd.github+json",
              "X-GitHub-Api-Version": "2022-11-28",
            },
          });

          // Update local data
          const todoData = await loadTodoData();

          // Initialize if not exists
          if (!todoData.issues[issueKey]) {
            todoData.issues[issueKey] = {
              status: status || "backlog",
              notes: "",
              lastUpdated: new Date().toISOString(),
            };
          }

          // Update values if provided
          if (status) todoData.issues[issueKey].status = status;
          if (note !== null) {
            // Get current date in YYYY-MM-DD format for the timestamp
            const today = new Date().toISOString().split('T')[0];

            // Append new note with timestamp instead of overwriting
            const existingNotes = todoData.issues[issueKey].notes || "";
            const separator = existingNotes.length > 0 ? "\n\n" : "";
            todoData.issues[issueKey].notes = existingNotes + separator + `[${today}] ${note}`;
          }

          todoData.issues[issueKey].lastUpdated = new Date().toISOString();

          // Add issue details if API call was successful
          if (issueResponse.ok) {
            const issueData = await issueResponse.json();
            todoData.issues[issueKey].title = issueData.title;
            todoData.issues[issueKey].url = issueData.html_url;
            console.log(`Retrieved title: "${issueData.title}"`);
          }

          await saveTodoData(todoData);
          console.log(`Updated local data for ${issueKey}`);

          // Show the todo list after updating
          if (Deno.args.includes("--show") || Deno.args.includes("--todo")) {
            // Continue execution to show the todo list
            todoMode = true;
          } else {
            console.log("\nTip: Use --show or --todo flag to see your updated to-do list");
            Deno.exit(0);
          }

          Deno.exit(0);
        } catch (error) {
          console.error(`Error fetching issue details: ${(error as Error).message}`);

          // Still update local data even if fetching details failed
          try {
            const todoData = await loadTodoData();

            // Make sure issues object exists
            if (!todoData.issues) {
              todoData.issues = {};
            }

            // Initialize if not exists
            if (!todoData.issues[issueKey]) {
              todoData.issues[issueKey] = {
                status: status || "backlog",
                notes: "",
                lastUpdated: new Date().toISOString(),
              };
            }

            // Update values if provided
            if (status) todoData.issues[issueKey].status = status;
            if (note !== null) {
              // Get current date in YYYY-MM-DD format for the timestamp
              const today = new Date().toISOString().split('T')[0];

              // Append new note with timestamp instead of overwriting
              const existingNotes = todoData.issues[issueKey].notes || "";
              const separator = existingNotes.length > 0 ? "\n\n" : "";
              todoData.issues[issueKey].notes = existingNotes + separator + `[${today}] ${note}`;
            }

            todoData.issues[issueKey].lastUpdated = new Date().toISOString();
            await saveTodoData(todoData);

            console.log(`Updated local data for ${issueKey} (without fetching title)`);
          } catch (saveError) {
            console.error(`Error saving local data: ${(saveError as Error).message}`);
          }

          Deno.exit(0);
        }
      } else {
        console.error("Invalid issue key format. Use 'owner/repo#number'");
        Deno.exit(1);
      }
    }

    // Load todo data if in todo mode
    let todoData: TodoStorage | undefined;
    if (todoMode) {
      todoData = await loadTodoData();
    }

    // Fetch GitHub views for standard and todo modes
    const views = await fetchAllViews(orgFilter);
    const totalItems = views.todo.length + views.toReview.length +
      views.done.length + views.reviewed.length;

    if (totalItems === 0 && !todoMode) {
      console.log(`No items found${orgFilter ? ` for ${orgFilter}` : ""}`);
    } else if (todoMode) {
      let hasAssignedIssues = views.todo.length > 0;
      let hasStoredItems = todoData && Object.keys(todoData.issues).length > 0;

      if (!hasAssignedIssues && !hasStoredItems) {
        console.log("No to-do items found.");
        console.log("Use 'poke --update=org/repo#123 --note=\"Your note\"' to add items.");
        Deno.exit(0);
      }

      console.log(`\n📋 Your To-Do List:\n`);

      // Track assigned issues to avoid duplicates
      const assignedKeys = new Set<string>();

      // Create grouped collections
      const inProgress: GitHubIssue[] = [];
      const blocked: GitHubIssue[] = [];
      const review: GitHubIssue[] = [];
      const backlog: GitHubIssue[] = [];

      // Group assigned issues by status
      if (views.todo.length > 0) {
        views.todo.forEach((item) => {
          const key = getIssueKey(item);
          assignedKeys.add(key);

          const status = todoData?.issues[key]?.status || "backlog";

          if (status === "in-progress") inProgress.push(item);
          else if (status === "blocked") blocked.push(item);
          else if (status === "review") review.push(item);
          else backlog.push(item);
        });
      }

      // Add manually tracked items that aren't assigned
      const manualItems: Record<string, { key: string; data: TodoItem }[]> = {
        "in-progress": [],
        "blocked": [],
        "review": [],
        "backlog": [],
      };

      if (todoData) {
        for (const [key, item] of Object.entries(todoData.issues)) {
          if (!assignedKeys.has(key)) {
            const status = item.status || "backlog";
            if (!manualItems[status]) manualItems[status] = [];
            manualItems[status].push({ key, data: item });
          }
        }
      }

      // Display in-progress items
      if (inProgress.length > 0 || manualItems["in-progress"].length > 0) {
        console.log(`\n🟨 In Progress (${inProgress.length + manualItems["in-progress"].length}):\n`);
        inProgress.forEach((item) => displayItem(item, "todo", todoData));
        manualItems["in-progress"].forEach(({ key, data }) => displayStoredItem(key, data));
      }

      // Display blocked items
      if (blocked.length > 0 || manualItems["blocked"].length > 0) {
        console.log(`\n🟥 Blocked (${blocked.length + manualItems["blocked"].length}):\n`);
        blocked.forEach((item) => displayItem(item, "todo", todoData));
        manualItems["blocked"].forEach(({ key, data }) => displayStoredItem(key, data));
      }

      // Display review items
      if (review.length > 0 || manualItems["review"].length > 0) {
        console.log(`\n🟦 Ready for Review (${review.length + manualItems["review"].length}):\n`);
        review.forEach((item) => displayItem(item, "todo", todoData));
        manualItems["review"].forEach(({ key, data }) => displayStoredItem(key, data));
      }

      // Display backlog items
      if (backlog.length > 0 || manualItems["backlog"].length > 0) {
        console.log(`\n⬜ Backlog (${backlog.length + manualItems["backlog"].length}):\n`);
        backlog.forEach((item) => displayItem(item, "todo", todoData));
        manualItems["backlog"].forEach(({ key, data }) => displayStoredItem(key, data));
      }

      // Display command examples
      console.log("\nCommands:");
      console.log("  Update status: poke --update=org/repo#123 --status=in-progress");
      console.log('  Add note:      poke --update=org/repo#123 --note="Working on this now"');
    } else {
      // Standard display
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
        views.todo.forEach((item) => displayItem(item, "todo", todoData));
      }

      // Display recently completed items if any
      if (views.done.length > 0) {
        console.log(`\n✓ ${views.done.length} Issues recently completed:\n`);
        views.done.slice(0, 5).forEach((item) => displayItem(item, "done", todoData));
      }

      // Display recently reviewed PRs
      if (views.reviewed.length > 0) {
        console.log(
          `\n👍 ${views.reviewed.length} Pull Requests you've reviewed:\n`,
        );
        views.reviewed.slice(0, 5).forEach((item) => displayItem(item, "reviewed", todoData));
      }

      // Display PRs where the user is mentioned
      if (views.mentioned.length > 0) {
        console.log(
          `\n💬 ${views.mentioned.length} Pull Requests where you're mentioned:\n`,
        );
        views.mentioned.forEach((item) => displayItem(item, "mentioned", todoData));
      }

      // Display todo mode hint if not in todo mode and has assigned issues
      if (!todoMode && views.todo.length > 0) {
        console.log("\nTip: Use 'poke --todo' to see issues as a to-do list with status tracking");
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
        const title = `GitHub: ${views.toReview.length} PR${views.toReview.length !== 1 ? "s" : ""} to review, ${views.todo.length} issue${views.todo.length !== 1 ? "s" : ""} assigned`;

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
