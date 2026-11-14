package cli

import (
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// Verbs are the kubectl-style verbs, in help order. A verb command appears
// only if at least one resource supports it.
var Verbs = []struct{ Name, Short string }{
	{"get", "Display one or many resources"},
	{"describe", "Show details of a specific resource"},
	{"create", "Create a resource"},
	{"delete", "Delete resources"},
	{"tag", "Add or remove tags on a resource"},
	{"sync", "Fetch resources from their source and store them"},
}

// Resource is a kind of object that eoy manages, such as "evidence" or
// "notes". To add one, return it from Resources in resources.go.
type Resource struct {
	// Name is the singular name, for example "note".
	Name string
	// Plural is the plural name, for example "notes". It is the canonical
	// name in help and in "eoy api-resources".
	Plural string
	// ShortNames are extra aliases, for example "ev".
	ShortNames []string
	// Commands maps a verb from Verbs to a function that returns the leaf
	// command. The leaf's Use gives only the arguments after the resource
	// name, for example "[NAME...]". Its Short, flags and RunE are its own.
	Commands map[string]func() *cobra.Command
}

// names returns every name that selects the resource.
func (r Resource) names() []string {
	return append([]string{r.Name, r.Plural}, r.ShortNames...)
}

// newVerbCmds returns one command per verb that a resource supports. Each
// resource becomes a subcommand of the verb, so that "eoy get pullrequests",
// "eoy get pullrequest" and "eoy get pr" are the same command.
func newVerbCmds(resources []Resource) []*cobra.Command {
	var cmds []*cobra.Command
	for _, v := range Verbs {
		verb := &cobra.Command{
			Use:   v.Name + " RESOURCE",
			Short: v.Short,
			Args:  unknownSubcommand("resource type"),
			RunE: func(cmd *cobra.Command, _ []string) error {
				return usageErrorf("you must specify the type of resource to %s; valid types: %s",
					v.Name, strings.Join(resourceTypes(cmd), ", "))
			},
		}
		for _, r := range resources {
			newLeaf, ok := r.Commands[v.Name]
			if !ok {
				continue
			}
			leaf := newLeaf()
			leaf.Use = strings.TrimSpace(r.Plural + " " + leaf.Use)
			for _, alias := range append([]string{r.Name}, r.ShortNames...) {
				if alias != r.Plural {
					leaf.Aliases = append(leaf.Aliases, alias)
				}
			}
			verb.AddCommand(leaf)
		}
		if verb.HasSubCommands() {
			cmds = append(cmds, verb)
		}
	}
	return cmds
}

func resourceTypes(verb *cobra.Command) []string {
	var names []string
	for _, c := range verb.Commands() {
		names = append(names, c.Name())
	}
	return names
}

// newAPIResourcesCmd returns "eoy api-resources", which lists the resources,
// their short names and their verbs, as kubectl does.
func newAPIResourcesCmd(resources []Resource) *cobra.Command {
	var noHeaders bool
	cmd := &cobra.Command{
		Use:   "api-resources",
		Short: "Print the supported resource types",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rs := slices.Clone(resources)
			slices.SortFunc(rs, func(a, b Resource) int { return strings.Compare(a.Plural, b.Plural) })
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 3, ' ', 0)
			if !noHeaders {
				fmt.Fprintln(w, "NAME\tSHORTNAMES\tVERBS")
			}
			for _, r := range rs {
				var verbs []string
				for _, v := range Verbs {
					if _, ok := r.Commands[v.Name]; ok {
						verbs = append(verbs, v.Name)
					}
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", r.Plural, strings.Join(r.ShortNames, ","), strings.Join(verbs, ","))
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&noHeaders, "no-headers", false, "do not print the header row")
	return cmd
}
