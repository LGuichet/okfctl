// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"fmt"

	"github.com/cwest/okfctl/internal/okf"
	"github.com/spf13/cobra"
)

// newNodeLinkCmd builds `node link`: add one cross-reference from one node to
// another without hand-editing the body.
func newNodeLinkCmd() *cobra.Command {
	var bundle, section string
	var dry bool
	c := &cobra.Command{
		Use:   "link <from> <to>",
		Short: "Add a Markdown link from one node to another (idempotent)",
		Long: "link adds a cross-reference from <from> to <to>: a bullet holding a relative " +
			"Markdown link (OKF §6.1) whose text is <to>'s title, filed under the \"## Related\" " +
			"heading of <from>'s body (or --section), which is created at the end of the body if " +
			"absent. The heading placement is an okfctl authoring convention, not a spec rule. " +
			"A heading inside a fenced code block is ignored. The frontmatter is preserved " +
			"byte-for-byte and `modified` is not bumped. If <from> already links to <to> in any " +
			"spelling (relative, ./, /-absolute, with a #fragment) the command is a no-op. Both " +
			"paths must stay inside the bundle (OKF §3), <to> must exist, and neither end may be " +
			"a reserved index.md or log.md (OKF §3.1). A successful link is recorded in log.md " +
			"(OKF §9). The .md suffix is optional. --dry-run prints the change and writes nothing.",
		Example: "  # Link two nodes under ## Related\n" +
			"  okfctl node link concepts/revenue concepts/net-revenue\n\n" +
			"  # File the link under another heading\n" +
			"  okfctl node link concepts/revenue concepts/net-revenue --section \"See also\"\n\n" +
			"  # Preview the change without touching disk\n" +
			"  okfctl node link concepts/revenue concepts/net-revenue --dry-run",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := okf.Load(bundle)
			if err != nil {
				return err
			}
			from, to := withMD(args[0]), withMD(args[1])
			p, err := okf.PlanLink(bundle, b, from, to, section)
			if err != nil {
				return fmt.Errorf("node link: %w", err)
			}
			out := cmd.OutOrStdout()
			if p.Exists {
				fmt.Fprintf(out, "%s already links to %s (%s); nothing to do\n", p.From, p.To, p.Existing)
				return nil
			}
			state := "existing section"
			if p.CreatesSection {
				state = "new section"
			}
			if dry {
				fmt.Fprintf(out, "link %s -> %s\n", p.From, p.To)
				fmt.Fprintf(out, "  under \"## %s\" (%s) in %s\n", p.Section, state, p.From)
				fmt.Fprintf(out, "  + %s\n", p.Line)
				return nil
			}
			if err := okf.ApplyLink(p); err != nil {
				return fmt.Errorf("node link: %w", err)
			}
			logOnLink(cmd, bundle, p.From, p.To)
			fmt.Fprintf(out, "Linked %s -> %s under \"## %s\" (%s)\n", p.From, p.To, p.Section, state)
			return nil
		},
	}
	c.Flags().StringVar(&bundle, "bundle", ".", "bundle directory to operate on")
	c.Flags().StringVar(&section, "section", okf.DefaultLinkSection, "\"##\" heading text to file the link under (created if absent)")
	c.Flags().BoolVar(&dry, "dry-run", false, "print the change without touching disk")
	return c
}
