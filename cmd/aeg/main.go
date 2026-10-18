// Package main is the entry point of the aeg command.
package main

import (
	"context"
	"errors"
	"os"

	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"
)

func main() {
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()

	cmd := &cli.Command{
		Name:  "aeg",
		Usage: "Agent Egress Gateway: a credential and policy proxy for AI agents",
		Commands: []*cli.Command{
			{
				Name:      "run",
				Usage:     "Start the proxy, then run a command that uses it",
				ArgsUsage: "-- <command> [args]",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "config", Usage: "configuration file", Required: true},
					&cli.StringFlag{Name: "log", Usage: "request log file (default: $XDG_STATE_HOME/aeg/<run>.log)"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return errors.New("missing command: aeg run -- <command> [args]")
					}

					code, err := run(ctx, c.String("config"), c.String("log"), c.Args().Slice())
					if err != nil {
						return err
					}

					return cli.Exit("", code)
				},
			},
		},
	}

	if err := cmd.Run(logger.WithContext(context.Background()), os.Args); err != nil {
		logger.Fatal().Err(err).Msg("Command failed")
	}
}
