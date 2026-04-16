package util

import (
	"os"

	"github.com/spf13/cobra"
)

func CompletionCommand() *cobra.Command {
	return &cobra.Command{
		Use:                   "completion [bash|zsh]",
		Short:                 "Generate completion script for the specified shell",
		DisableFlagsInUseLine: true,
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs:             []string{"bash", "zsh"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			default:
				return cmd.Help()
			}
		},
	}
}

func IsCompletionCommand(name string) bool {
	return name == "completion" || name == "__complete"
}
