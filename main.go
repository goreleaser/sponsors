package main

import (
	"context"
	"os"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:           "sponsors",
		Short:         "Manage sponsor lists",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newGenerateCmd(), newApplyCmd())
	if err := fang.Execute(context.Background(), root); err != nil {
		os.Exit(1)
	}
}
