package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/acarl005/stripansi"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func GenMarkdownTree(cmd *cobra.Command, dir string) error {
	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand() {
			continue
		}
		if err := GenMarkdownTree(c, dir); err != nil {
			return err
		}
	}

	basename := makeBasename(cmd.CommandPath())
	filename := filepath.Join(dir, basename)
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := GenMarkdownCustom(cmd, f); err != nil {
		return err
	}
	return nil
}

// GenMarkdownCustom creates custom markdown output.
func GenMarkdownCustom(cmd *cobra.Command, w io.Writer) error {
	cmd.InitDefaultHelpCmd()
	cmd.InitDefaultHelpFlag()

	buf := new(bytes.Buffer)
	name := cmd.CommandPath()

	buf.WriteString(heredoc.Doc(`
---
editLink: false
---

`))

	buf.WriteString("# " + name + "\n\n")
	buf.WriteString(cmd.Short + "\n\n")
	if len(cmd.Long) > 0 {
		buf.WriteString("## Synopsis\n\n")
		buf.WriteString(cmd.Long + "\n\n")
	}

	if cmd.Runnable() {
		buf.WriteString(fmt.Sprintf("```\n%s\n```\n\n", cmd.UseLine()))
	}

	if len(cmd.Example) > 0 {
		buf.WriteString("## Examples\n\n")
		buf.WriteString(fmt.Sprintf("```shellsession\n%s\n```\n\n", trimEachLine(stripansi.Strip(cmd.Example))))
	}

	if err := printOptions(buf, cmd, name); err != nil {
		return err
	}
	if hasSeeAlso(cmd) {
		buf.WriteString("## See also\n\n")
		if cmd.HasParent() {
			parent := cmd.Parent()
			pname := parent.CommandPath()
			link := makeBasename(pname)
			buf.WriteString(fmt.Sprintf("* [%s](%s)\t - %s\n", pname, link, parent.Short))
			cmd.VisitParents(func(c *cobra.Command) {
				if c.DisableAutoGenTag {
					cmd.DisableAutoGenTag = c.DisableAutoGenTag
				}
			})
		}

		children := cmd.Commands()
		sort.Sort(byName(children))

		for _, child := range children {
			if !child.IsAvailableCommand() || child.IsAdditionalHelpTopicCommand() {
				continue
			}
			cname := name + " " + child.Name()
			link := makeBasename(cname)
			buf.WriteString(fmt.Sprintf("* [%s](%s)\t - %s\n", cname, link, child.Short))
		}
		buf.WriteString("\n")
	}
	_, err := buf.WriteTo(w)
	return err
}

func makeBasename(commandPath string) string {
	basename := strings.ReplaceAll(commandPath, " ", "-") + ".md"
	basename = strings.TrimPrefix(basename, "ortfodb-")
	if basename == "ortfodb.md" {
		return "global-options.md"
	}
	return basename
}

type byName []*cobra.Command

func (s byName) Len() int           { return len(s) }
func (s byName) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s byName) Less(i, j int) bool { return s[i].Name() < s[j].Name() }

// Test to see if we have a reason to print See Also information in docs
// Basically this is a test for a parent command or a subcommand which is
// both not deprecated and not the autogenerated help command.
func hasSeeAlso(cmd *cobra.Command) bool {
	if cmd.HasParent() {
		return true
	}
	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand() {
			continue
		}
		return true
	}
	return false
}

func printOptions(buf *bytes.Buffer, cmd *cobra.Command, name string) error {
	flags := cmd.NonInheritedFlags()
	flags.SetOutput(buf)
	if flags.HasAvailableFlags() {
		buf.WriteString("## Options\n\n")
		printFlagGroupOptions(flags, buf)
		buf.WriteRune('\n')
	}

	parentFlags := cmd.InheritedFlags()
	parentFlags.SetOutput(buf)
	if parentFlags.HasAvailableFlags() {
		buf.WriteString("## Options inherited from parent commands\n\n")
		printFlagGroupOptions(parentFlags, buf)
		buf.WriteRune('\n')
	}
	return nil
}

func printFlagGroupOptions(f *pflag.FlagSet, buf *bytes.Buffer) string {
	lines := make([]string, 0)

	buf.WriteString(
		"| Shorthand | Flag | Argument | Description | Default value |\n" +
			"| --- | --- | --- | --- | --- |\n",
	)

	f.VisitAll(func(flag *pflag.Flag) {
		if flag.Hidden {
			return
		}

		line := ""
		// We use &hyphen;&hyphen; instead of -- to avoid markdown "smarty pants" conversion of -- into &mdash;/&ndash;
		if flag.Shorthand != "" && flag.ShorthandDeprecated == "" {
			line = fmt.Sprintf("| -%s | &hyphen;&hyphen;%s ", flag.Shorthand, flag.Name)
		} else {
			line = fmt.Sprintf("| | &hyphen;&hyphen;%s ", flag.Name)
		}

		varname, usage := pflag.UnquoteUsage(flag)
		if varname != "" {
			line += fmt.Sprintf("| %s ", varname)
		} else {
			line += "| "
		}

		if flag.NoOptDefVal != "" {
			switch flag.Value.Type() {
			case "string":
				line += fmt.Sprintf("[=\"%s\"]", flag.NoOptDefVal)
			case "bool":
				if flag.NoOptDefVal != "true" {
					line += fmt.Sprintf("[=%s]", flag.NoOptDefVal)
				}
			case "count":
				if flag.NoOptDefVal != "+1" {
					line += fmt.Sprintf("[=%s]", flag.NoOptDefVal)
				}
			default:
				line += fmt.Sprintf("[=%s]", flag.NoOptDefVal)
			}
		}

		// This special character will be replaced with spacing once the
		// correct alignment is calculated

		line += fmt.Sprintf("| %s ", usage)
		if len(flag.Deprecated) != 0 {
			line += fmt.Sprintf(" **(DEPRECATED: %s)**", flag.Deprecated)
		}
		if !defaultIsZeroValue(flag) {
			line += fmt.Sprintf("| %s", flag.DefValue)
		}

		lines = append(lines, line)
	})

	for _, line := range lines {
		buf.WriteString(line + "\n")
	}

	return buf.String()
}

func trimEachLine(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}

