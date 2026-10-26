package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// These expectations were captured from the pre-Go CLI once. Running this
// regression test requires neither Deno nor the TypeScript implementation.
func TestCommandContract(t *testing.T) {
	data, err := os.ReadFile("testdata/pre_go_commands.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Args     []string `json:"args"`
		Stdout   string   `json:"stdout"`
		Stderr   string   `json:"stderr"`
		ExitCode int      `json:"exit_code"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	snapshot, err := os.ReadFile("../testdata/pre_go.sql")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "poke.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(snapshot)); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Cases deliberately share the database: they exercise the original
	// sequence of note edits and task lifecycle operations.
	for _, tc := range cases {
		var out, errOut bytes.Buffer
		c := offlineCLI(t, &out, &errOut)
		args := append([]string{"--db", path}, tc.Args...)
		exitCode := 0
		if err := c.Run(context.Background(), args); err != nil {
			fmt.Fprintln(&errOut, "Error:", err)
			exitCode = 1
		}
		if out.String() != tc.Stdout || errOut.String() != tc.Stderr || exitCode != tc.ExitCode {
			t.Errorf("%v\nstdout=%q, want %q\nstderr=%q, want %q\nexit=%d, want %d",
				tc.Args, &out, tc.Stdout, &errOut, tc.Stderr, exitCode, tc.ExitCode)
		}
	}
}
