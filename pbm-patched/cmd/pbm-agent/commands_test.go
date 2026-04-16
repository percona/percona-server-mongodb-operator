package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/percona/percona-backup-mongodb/pbm/version"
)

func TestRootCmd_NoArgs(t *testing.T) {
	rootCmd, _ := setupTestCmd()
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "required flag mongodb-uri") {
		t.Fatal(err)
	}
}

func TestRootCmd_Config(t *testing.T) {
	tmpConfig, cleanup := createTempConfigFile(`
log:
  path: "/dev/stderr"
  level: "D"
  json: false
`)
	defer cleanup()

	rootCmd, _ := setupTestCmd("--config", tmpConfig)
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "required flag mongodb-uri") {
		t.Fatal(err)
	}
}

func createTempConfigFile(content string) (string, func()) {
	temp, _ := os.CreateTemp("", "test-config-*.yaml")

	_, _ = temp.WriteString(content)
	_ = temp.Close()

	return temp.Name(), func() {
		_ = os.Remove(temp.Name())
	}
}

func TestVersionCommand_Default(t *testing.T) {
	rootCmd, buf := setupTestCmd("version")
	err := rootCmd.Execute()
	if err != nil {
		t.Fatal(err)
	}

	output := buf.String()

	if !strings.Contains(output, "Version:") {
		t.Errorf("expected full version info in output, got: %s", output)
	}
}

func TestVersionCommand_Short(t *testing.T) {
	rootCmd, buf := setupTestCmd("version", "--short")
	err := rootCmd.Execute()
	if err != nil {
		t.Fatal(err)
	}

	output := buf.String()

	if !strings.Contains(output, version.Current().Short()) {
		t.Errorf("expected short version info in output, got: %s", output)
	}
}

func setupTestCmd(args ...string) (*cobra.Command, *bytes.Buffer) {
	cmd := rootCommand()
	cmd.AddCommand(versionCommand())

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)

	return cmd, &buf
}
