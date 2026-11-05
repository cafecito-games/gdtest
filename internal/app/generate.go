package app

import (
	"errors"
	"fmt"

	"github.com/cafecito-games/gdtest/internal/config"
	"github.com/cafecito-games/gdtest/internal/doubles"
	"github.com/cafecito-games/gdtest/internal/version"
	"github.com/spf13/cobra"
)

type generateDoublesOptions struct {
	project string
	check   bool
}

func newGenerateCommand(streams IO, exitCode *int) *cobra.Command {
	command := &cobra.Command{
		Use:   "generate",
		Short: "Generate source files from project configuration",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return errors.New("generate requires a target: gdtest generate doubles")
		},
	}
	options := generateDoublesOptions{}
	doublesCommand := &cobra.Command{
		Use:   "doubles",
		Short: "Generate typed test doubles declared in gdtest.json",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			*exitCode = generateDoubles(options, streams)
		},
	}
	addProjectFlag(doublesCommand, &options.project)
	doublesCommand.Flags().BoolVar(&options.check, "check", false, "report out-of-date doubles without changing files")
	command.AddCommand(doublesCommand)
	return command
}

func generateDoubles(options generateDoublesOptions, streams IO) int {
	root, err := projectRoot(options.project)
	if err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	project, err := config.Load(root)
	if err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	plan, diagnostics := doubles.Prepare(root, project, version.Product)
	if len(diagnostics) > 0 {
		for _, diagnostic := range diagnostics {
			if _, err := fmt.Fprintln(streams.Err, diagnostic.String()); err != nil {
				return ExitError
			}
		}
		return ExitError
	}
	if options.check {
		findings := plan.Findings()
		for _, finding := range findings {
			if _, err := fmt.Fprintln(streams.Out, finding); err != nil {
				writeDiagnostic(streams.Err, "gdtest:", err)
				return ExitError
			}
		}
		if len(findings) > 0 {
			return ExitFailures
		}
		_, err := fmt.Fprintln(streams.Out, "0 double(s) out of date")
		if err != nil {
			writeDiagnostic(streams.Err, "gdtest:", err)
			return ExitError
		}
		return ExitOK
	}
	written, removed, err := doubles.Write(root, plan)
	if err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	if _, err := fmt.Fprintf(streams.Out, "%d file(s) written; %d file(s) removed\n", written, removed); err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	return ExitOK
}
