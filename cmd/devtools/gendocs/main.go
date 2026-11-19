// Command gendocs regenerates the marker-delimited regions of SYSTEM.md and README.md: the tool
// catalogue from the live ToolModule declarations (P5-5) and the channel capability matrix from
// the registered adapters (V4-34).
//
//	go run ./cmd/devtools/gendocs          # rewrite the docs in place
//	go run ./cmd/devtools/gendocs -check   # exit 1 if they are stale (CI also enforces this
//	                                       # through internal/docgen TestDocsAreUpToDate)
//
// Run it from the repository root.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/nivik/mypa/internal/docgen"
)

func main() {
	check := flag.Bool("check", false, "report stale files and exit non-zero instead of writing")
	flag.Parse()

	regions, err := docgen.Regions()
	if err != nil {
		fail("%v", err)
	}
	stale := 0
	for _, name := range docgen.DocFiles {
		raw, err := os.ReadFile(name)
		if err != nil {
			fail("read %s: %v (run from the repository root)", name, err)
		}
		updated, err := docgen.Apply(string(raw), regions)
		if err != nil {
			fail("%s: %v", name, err)
		}
		if updated == string(raw) {
			continue
		}
		stale++
		if *check {
			fmt.Printf("%s is stale\n", name)
			continue
		}
		if err := os.WriteFile(name, []byte(updated), 0o644); err != nil {
			fail("write %s: %v", name, err)
		}
		fmt.Printf("updated %s\n", name)
	}
	if *check && stale > 0 {
		os.Exit(1)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gendocs: "+format+"\n", args...)
	os.Exit(2)
}
