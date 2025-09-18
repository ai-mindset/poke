export async function debug(
  context: Record<string, unknown> = {},
): Promise<void> {
  const encoder = new TextEncoder();
  const decoder = new TextDecoder();

  console.log("\n=== DEBUGGER ACTIVE ===");
  console.log("Available variables:", Object.keys(context).join(", "));

  // Set up global context for evaluation
  const globalContext = {
    ...context,
    print: console.log,
    inspect: (obj: unknown) => console.dir(obj, { depth: null }),
  };

  let running = true;
  while (running) {
    Deno.stdout.writeSync(encoder.encode("\n[debug]> "));
    const buf = new Uint8Array(1024);
    const bytesRead = Deno.stdin.readSync(buf);

    if (bytesRead === null) {
      console.log("End of input reached. Exiting debugger.");
      break;
    }

    const input = decoder.decode(buf.subarray(0, bytesRead)).trim();

    if (input === "c" || input === "continue") {
      running = false;
    } else if (input === "h" || input === "help") {
      console.log(`Commands:
  c, continue    Continue execution
  h, help        Show this help
  v, vars        Show all variables
  q, quit        Exit program
  ?var           Inspect variable
  <expression>   Evaluate JS expression`);
    } else if (input === "v" || input === "vars") {
      for (const [key, value] of Object.entries(globalContext)) {
        if (key !== "print" && key !== "inspect") {
          console.log(`${key} =`, value);
        }
      }
    } else if (input === "q" || input === "quit") {
      Deno.exit(1);
    } else if (input.startsWith("$")) {
      // Use Deno.Command instead of Deno.run
      try {
        const expr = input.slice(1).trim();
        const command = new Deno.Command("deno", {
          args: ["eval", `console.log(${expr})`],
          stdout: "piped",
          stderr: "piped",
        });

        const { stdout, stderr, success } = await command.output();

        if (success) {
          console.log(decoder.decode(stdout).trim());
        } else {
          console.error(decoder.decode(stderr).trim());
        }
      } catch (error) {
        console.error("Error:", (error as Error).message);
      }
    } else if (input.startsWith("?")) {
      const varName = input.slice(1).trim();
      if (varName in globalContext) {
        console.dir(globalContext[varName], { depth: null });
      } else {
        console.log(`Variable '${varName}' not found`);
      }
    } else {
      try {
        const evalFn = new Function(
          ...Object.keys(globalContext),
          `
          try { return ${input}; }
          catch (e) { return "Error: " + e.message; }
        `,
        );
        console.log(evalFn(...Object.values(globalContext)));
      } catch (error) {
        console.error("Evaluation error:", error.message);
      }
    }
  }
  console.log("=== CONTINUING EXECUTION ===\n");
}

export function inspect<T>(label: string, value: T): T {
  console.log(`[${label}]:`, value);
  return value;
}

export async function breakpoint(
  ...additionalVars: Record<string, unknown>[]
): Promise<void> {
  const stack = new Error().stack?.split("\n").slice(1).join("\n");
  console.log("Breakpoint hit at:", stack);
  await debug(Object.assign({}, ...additionalVars));
}
