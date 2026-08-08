#!/usr/bin/env -S deno run --allow-env --allow-net --allow-read --allow-write

import { main } from "./src/cli.ts";

if (import.meta.main) {
  await main(Deno.args);
}
