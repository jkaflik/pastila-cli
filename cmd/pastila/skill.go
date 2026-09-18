package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/jkaflik/pastila-cli/internal/skill"
)

func runSkill(args []string) int {
	if len(args) == 0 {
		printSkillUsage()
		return 2
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printSkillUsage()
		return 0
	}
	if args[0] != "install" {
		printf("Unknown skill command: %s\n", args[0])
		printSkillUsage()
		return 2
	}

	fs := flag.NewFlagSet("skill install", flag.ContinueOnError)
	fs.SetOutput(printWriter)
	targetName := fs.String("target", string(skill.TargetAll), "Install target: all, portable, or claude")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		printf("Unexpected arguments\n")
		return 2
	}

	target := skill.Target(*targetName)
	if !target.Valid() {
		printf("Invalid skill target %q; expected all, portable, or claude\n", *targetName)
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		printf("Failed to resolve home directory: %v\n", err)
		return 1
	}

	results, installErr := skill.Install(home, target)
	for _, result := range results {
		switch result.Status {
		case skill.StatusInstalled:
			printf("Installed Pastila skill: %s\n", result.Path)
		case skill.StatusUpdated:
			printf("Updated Pastila skill: %s\n", result.Path)
		case skill.StatusUnchanged:
			printf("Pastila skill already up to date: %s\n", result.Path)
		}
	}
	if installErr != nil {
		printf("Failed to install Pastila skill: %v\n", installErr)
		return 1
	}

	return 0
}

func printSkillUsage() {
	_, _ = fmt.Fprintln(printWriter, "Usage: pastila skill install [-target all|portable|claude]")
}
