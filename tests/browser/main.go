// Walter's test-only browser tool uses the shared Archaic Go visualtest library.
// This is a separate module: the runtime never depends on Rod.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/archaic-work/visual-test/visualtest"
	rodtest "github.com/archaic-work/visual-test/visualtest/rod"
)

// Navigate returns before the document body necessarily exists. Rod's
// element lookup waits for the expected view without throwing on a missing body.
func waitView(page *rod.Page, name string) error {
	_, err := page.Element(fmt.Sprintf("body[data-view=%q]", name))
	if err != nil {
		return fmt.Errorf("waiting for view %q: %w", name, err)
	}
	return nil
}

func waitFor(page *rod.Page, expression string) error { return page.Wait(rod.Eval(expression)) }
func assertPage(page *rod.Page, expression, description string) error {
	result, err := page.Eval(expression)
	if err != nil {
		return fmt.Errorf("%s: %w", description, err)
	}
	if !result.Value.Bool() {
		return fmt.Errorf("assertion failed: %s", description)
	}
	return nil
}

// Scenario fixtures use the same transport implementation. Its exact
// source and the per-scenario assertions form each versioned scenario contract.
func suite() rodtest.Runner {
	serve := func(name string) func(context.Context, string) (string, func(), error) {
		return func(_ context.Context, assets string) (string, func(), error) {
			srv, err := fixtureServer(name, assets)
			if err != nil {
				return "", nil, err
			}
			return srv.URL, srv.Close, nil
		}
	}
	contract := []string{"tests/browser/fixture.go", "tests/browser/main.go"}
	return rodtest.Runner{
		Browser:   os.Getenv("WALTER_BROWSER_EXECUTABLE"),
		NoSandbox: os.Getenv("WALTER_BROWSER_NO_SANDBOX") == "1",
		Locale:    "de-DE", Timezone: "Europe/Berlin", Timeout: 15 * time.Second,
		Viewports: map[string]visualtest.Size{
			"desktop": {Width: 1071, Height: 1579},
			"mobile":  {Width: 390, Height: 844},
		},
		Cases: map[string]rodtest.Case{
			"chat.start.empty": {
				Fixture: "empty-chat", ContractFiles: contract, Serve: serve("empty-chat"),
				InitScript: "Math.random = () => 0.25;",
				Run: func(_ context.Context, page *rod.Page, shot func(string) error) error {
					if err := waitView(page, "start"); err != nil {
						return err
					}
					if err := assertPage(page, `() => document.querySelector('#content').value === '' &&
                      !document.querySelector('#content').disabled &&
                      !document.querySelector('#quick').hidden &&
                      !document.querySelector('#thoughtful').hidden`, "empty welcome composer and modes"); err != nil {
						return err
					}
					return shot("ready")
				},
			},
			"chat.reply.streaming": {
				Fixture: "streaming-reply", ContractFiles: contract, Serve: serve("streaming-reply"),
				InitScript: "Math.random = () => 0.25;",
				Run: func(_ context.Context, page *rod.Page, shot func(string) error) error {
					if err := waitView(page, "start"); err != nil {
						return err
					}
					page.MustElement("#content").MustInput("Was ist Marmelade?")
					page.MustElement("#thoughtful").MustClick()
					if err := waitFor(page, `() => document.body?.dataset.view === 'conversation' &&
                        !!document.querySelector('.response-loader')`); err != nil {
						return err
					}
					if err := assertPage(page, `() => document.querySelectorAll('.message').length === 2 &&
                        document.querySelector('.response-status')?.textContent === 'Warte auf das Modell …' &&
                        document.querySelector('.response-loader')?.getAttribute('aria-label') === 'Walter bereitet die Antwort vor.' &&
                        !document.querySelector('#stop').hidden`, "pending stream and stop control"); err != nil {
						return err
					}
					return shot("waiting")
				},
			},
			"chat.reply.completed": {
				Fixture: "completed-reply", ContractFiles: contract, Serve: serve("completed-reply"),
				InitScript: "Math.random = () => 0.25;",
				Run: func(_ context.Context, page *rod.Page, shot func(string) error) error {
					if err := waitView(page, "start"); err != nil {
						return err
					}
					page.MustElement("#history-link").MustClick()
					if err := waitFor(page, `() => document.body?.dataset.view === 'history' && document.querySelectorAll('#chats li').length === 1`); err != nil {
						return err
					}
					page.MustElement("#chats li a").MustClick()
					if err := waitFor(page, `() => document.body?.dataset.view === 'conversation' && document.querySelector('#content')?.disabled === false && !!document.querySelector('.assistant pre')`); err != nil {
						return err
					}
					if err := assertPage(page, `() => document.querySelectorAll('.message').length === 2 &&
                        !!document.querySelector('.assistant .speaker img') &&
                        !!document.querySelector('.assistant table') &&
                        !!document.querySelector('.assistant pre code') &&
                        document.documentElement.scrollWidth <= innerWidth`, "completed answer with readable Markdown"); err != nil {
						return err
					}
					return shot("ready")
				},
			},
			"chat.history.populated": {
				Fixture: "history-with-conversations", ContractFiles: contract, Serve: serve("history-with-conversations"),
				InitScript: "Math.random = () => 0.25;",
				Run: func(_ context.Context, page *rod.Page, shot func(string) error) error {
					if err := waitView(page, "start"); err != nil {
						return err
					}
					page.MustElement("#history-link").MustClick()
					if err := waitFor(page, `() => document.body?.dataset.view === 'history' && document.querySelectorAll('#chats li').length === 2`); err != nil {
						return err
					}
					if err := assertPage(page, `() => document.querySelector('#chats').textContent.includes('Marmelade') &&
                        document.querySelector('#chats').textContent.includes('Tabellen')`, "two seeded history entries"); err != nil {
						return err
					}
					return shot("ready")
				},
			},
		},
	}
}

func revision(root string) (string, error) {
	b, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("resolve Git revision: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

func capture(args []string) error {
	fs := flag.NewFlagSet("capture", flag.ContinueOnError)
	out := fs.String("out", "", "output directory for screenshots and manifest")
	assets := fs.String("web-root", "", "checked out web assets (defaults to project web/)")
	baseline := fs.String("base-root", "", "main checkout to capture matching contracts")
	rev := fs.String("revision", "", "source revision (defaults to Git HEAD)")
	only := fs.String("scenario", "", "run a single scenario ID")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := filepath.Abs(filepath.Join(cwd, "..", ".."))
	if err != nil {
		return err
	}
	if *rev == "" {
		*rev, err = revision(root)
		if err != nil {
			return err
		}
	}
	if *assets == "" {
		*assets = filepath.Join(root, "web")
	}
	runner := suite()
	opts := rodtest.Options{ProjectRoot: root, Assets: *assets, Revision: *rev, Output: *out, Only: *only}
	candidate, err := runner.Capture(context.Background(), opts)
	if err != nil {
		return err
	}
	fmt.Printf("Captured %d real-Chromium checkpoints\n", len(candidate.Images))
	if *baseline == "" {
		return nil
	}
	if *out == "" {
		return errors.New("--base-root requires --out")
	}
	baseSHA, err := revision(*baseline)
	if err != nil {
		return err
	}
	baseOut := filepath.Join(*out, "..", "main")
	compareOut := filepath.Join(*out, "..", "report")
	main, err := runner.Capture(context.Background(), rodtest.Options{
		ProjectRoot: *baseline, Revision: baseSHA, Output: baseOut,
		Definitions: filepath.Join(*baseline, "tests", "scenarios"),
		Assets:      filepath.Join(*baseline, "web"), MatchContracts: candidate.Contracts,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Captured %d matching main checkpoints\n", len(main.Images))
	result, err := visualtest.Compare(baseOut, *out, compareOut)
	if err != nil {
		return err
	}
	fmt.Printf("Comparison: %v\n", result.Counts)
	return nil
}

func compare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	base := fs.String("base", "", "main captures")
	candidate := fs.String("candidate", "", "branch captures")
	out := fs.String("out", "", "comparison report")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *base == "" || *candidate == "" || *out == "" {
		return errors.New("--base, --candidate and --out are required")
	}
	summary, err := visualtest.Compare(*base, *candidate, *out)
	if err != nil {
		return err
	}
	fmt.Printf("Visual comparison: %v\n", summary.Counts)
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: go run . capture|compare [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "capture":
		err = capture(os.Args[2:])
	case "compare":
		err = compare(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command: %s", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
