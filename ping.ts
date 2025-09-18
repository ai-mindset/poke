#!/usr/bin/env deno run --allow-env --allow-net --allow-read

import { debug, inspect } from "./debugger.ts";

interface Notification {
  id: string;
  subject: { title: string; type: string; url: string };
  repository: { full_name: string };
  reason: string;
  unread: boolean;
}

let GITHUB_TOKEN = Deno.env.get("GITHUB_TOKEN");
let WORK_ORGS: string[] = [];

if (!GITHUB_TOKEN) {
  const projectName = Deno.cwd().split(/[\/\\]/).pop() || "ping";
  const isWindows = Deno.build.os === "windows";
  const homeDir = isWindows
    ? Deno.env.get("USERPROFILE") || "C:\\"
    : Deno.env.get("HOME") || Error("HOME environment variable not set");
  const configPath = isWindows
    ? `%USERPROFILE%\\AppData\\Local\\${projectName}\\.env`
    : `${homeDir}/.config/${projectName}/.env`;

  try {
    const envFile = await Deno.readTextFile(configPath);
    const match = envFile.match(/^GITHUB_TOKEN=(.+)$/m);
    GITHUB_TOKEN = match?.[1]?.replace(/[""]/g, "");
    const orgsMatch = envFile.match(/^WORK_ORGS=(.+)$/m);

    if (orgsMatch?.[1]) {
      WORK_ORGS = orgsMatch[1].replace(/[""]/g, "").split(",").map((o) =>
        o.trim()
      );
    }
  } catch {
    // File doesn't exist, try next path
  }
}

const PRIORITY_REASONS = [
  "mention",
  "team_mention",
  "review_requested",
  "assign",
  "subscribed",
];

async function fetchImportantNotifications(
  orgFilter?: string,
): Promise<Notification[]> {
  const headers = {
    "Authorization": `Bearer ${GITHUB_TOKEN}`,
    "Accept": "application/vnd.github+json",
  };

  const response = await fetch(
    "https://api.github.com/notifications?per_page=50",
    {
      headers: headers,
    },
  );

  if (!response.ok) {
    throw new Error(`GitHub API error: ${response.status}`);
  }

  const notifications: Notification[] = await response.json();

  return notifications.filter((n) =>
    n.unread &&
    (["PullRequest", "Issue", "Discussion"].includes(n.subject.type) ||
      PRIORITY_REASONS.includes(n.reason)) &&
    (!orgFilter || n.repository.full_name.startsWith(`${orgFilter}/`))
  );
}

function prioritiseNotifications(
  notifications: Notification[],
): Notification[] {
  const reasonScores: { [key: string]: number } = {
    "review_requested": 10,
    "mention": 9,
    "team_mention": 8,
    "assign": 7,
    "subscribed": 5,
    "author": 4,
    "comment": 3,
  };

  const defaultScore = 1;

  return [...notifications].sort((a, b) => {
    const isWorkRepoA = WORK_ORGS.some((org) =>
      a.repository.full_name.startsWith(`${org}/`)
    );
    const isWorkRepoB = WORK_ORGS.some((org) =>
      b.repository.full_name.startsWith(`${org}/`)
    );

    const workBoostA = isWorkRepoA ? 20 : 0;
    const workBoostB = isWorkRepoB ? 20 : 0;

    const scoreA = (reasonScores[a.reason] || defaultScore) + workBoostA;
    const scoreB = (reasonScores[b.reason] || defaultScore) + workBoostB;

    // First compare by score (higher first)
    if (scoreB !== scoreA) {
      return scoreB - scoreA;
    }

    // If scores are equal, PR types come first
    if (a.subject.type === "PullRequest" && b.subject.type !== "PullRequest") {
      return -1;
    }
    if (a.subject.type !== "PullRequest" && b.subject.type === "PullRequest") {
      return 1;
    }

    // Finally sort by repository name
    return a.repository.full_name.localeCompare(b.repository.full_name);
  });
}

function displayNotification(n: Notification): void {
  const icon = n.subject.type === "PullRequest" ? "🔄" : "💬";
  const url = n.subject.url.replace("api.github.com/repos/", "github.com/")
    .replace("/pulls/", "/pull/");

  console.log(`${icon} [${n.repository.full_name}] ${n.subject.title}`);
  console.log(`   ${n.reason} • ${url}\n`);
}

async function sendNotification(title: string, body: string): Promise<void> {
  const cmd = new Deno.Command("notify-send", {
    args: [title, body],
  });
  await cmd.output();
}

if (import.meta.main) {
  try {
    const orgFilter = Deno.args[0];
    const notifications = await fetchImportantNotifications(orgFilter);

    if (notifications.length === 0) {
      console.log(
        `No new notifications${orgFilter ? ` for ${orgFilter}` : ""}`,
      );
    } else {
      // Prioritise notifications
      const prioritisedNotifications = prioritiseNotifications(notifications);

      const displayLimit = 10;
      const limitedNotifications = prioritisedNotifications.slice(
        0,
        displayLimit,
      );

      console.log(`${notifications.length} important notification(s):\n`);
      prioritisedNotifications.forEach(displayNotification);

      // Send desktop notification with limited items
      const title = `${notifications.length} GitHub Notification${
        notifications.length !== 1 ? "s" : ""
      }`;
      const body = limitedNotifications.map((n) => {
        const icon = n.subject.type === "PullRequest" ? "🔄" : "💬";
        const isWork = WORK_ORGS.some((org) =>
          n.repository.full_name.startsWith(`${org}/`)
        );
        const workPrefix = isWork ? "[WORK] " : "";
        return `${icon} ${workPrefix}${n.repository.full_name}: ${
          n.subject.title.substring(0, 60)
        }${n.subject.title.length > 60 ? "..." : ""}`;
      }).join("\n");

      await sendNotification(title, body);
    }
  } catch (error) {
    console.error("Error:", (error as Error).message);
    Deno.exit(1);
  }
}
