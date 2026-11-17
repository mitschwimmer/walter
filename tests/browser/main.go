// Browser scenario capture and visual comparison are test-only tools, not Walter runtime dependencies.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

type scenario struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Fixture     string   `json:"fixture"`
	Checkpoints []string `json:"checkpoints"`
	Viewports   []string `json:"viewports"`
	Themes      []string `json:"themes"`
}

type captureEntry struct {
	ID             string `json:"id"`
	Checkpoint     string `json:"checkpoint"`
	Viewport       string `json:"viewport"`
	Theme          string `json:"theme"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	DefinitionHash string `json:"definitionHash"`
	File           string `json:"file"`
}

type manifest struct {
	Format            int            `json:"format"`
	Revision          string         `json:"revision"`
	Chromium          string         `json:"chromium"`
	FixtureSourceHash string         `json:"fixtureSourceHash"`
	Platform          string         `json:"platform"`
	Locale            string         `json:"locale"`
	Timezone          string         `json:"timezone"`
	DeviceScaleFactor int            `json:"deviceScaleFactor"`
	ReducedMotion     string         `json:"reducedMotion"`
	Images            []captureEntry `json:"images"`
}

type dimension struct{ width, height int }

var viewports = map[string]dimension{"desktop": {1071, 1579}, "mobile": {390, 844}}
var idRE = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
var segmentRE = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
var knownScenarios = map[string]struct{ fixture, checkpoint string }{
	"chat.start.empty":       {"empty-chat", "ready"},
	"chat.reply.streaming":   {"streaming-reply", "waiting"},
	"chat.history.populated": {"history-with-conversations", "ready"},
}

func hash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func readManifest(path string) (manifest, error) {
	var m manifest
	data, err := os.ReadFile(filepath.Join(path, "manifest.json"))
	if err == nil {
		err = json.Unmarshal(data, &m)
	}
	return m, err
}
func saveJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
func definitionDir(root string) string { return filepath.Join(root, "tests", "scenarios") }
func loadDefinitions(path string) ([]scenario, map[string]string, error) {
	files, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, map[string]string{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var result []scenario
	hashes := make(map[string]string)
	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(path, file.Name()))
		if err != nil {
			return nil, nil, err
		}
		var def scenario
		if err := json.Unmarshal(raw, &def); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", file.Name(), err)
		}
		if !idRE.MatchString(def.ID) || file.Name() != def.ID+".json" {
			return nil, nil, fmt.Errorf("invalid scenario ID or filename %q", file.Name())
		}
		if def.Description == "" || len(def.Checkpoints) != 1 || !segmentRE.MatchString(def.Checkpoints[0]) {
			return nil, nil, fmt.Errorf("invalid checkpoint or description in %s", def.ID)
		}
		implementation, ok := knownScenarios[def.ID]
		if !ok || implementation.fixture != def.Fixture || implementation.checkpoint != def.Checkpoints[0] {
			return nil, nil, fmt.Errorf("scenario %s has no matching browser implementation", def.ID)
		}
		if len(def.Viewports) == 0 || len(def.Themes) == 0 {
			return nil, nil, fmt.Errorf("scenario %s has no capture variants", def.ID)
		}
		for _, v := range def.Viewports {
			if _, ok := viewports[v]; !ok {
				return nil, nil, fmt.Errorf("unknown viewport %s", v)
			}
		}
		for _, v := range def.Themes {
			if v != "light" && v != "dark" {
				return nil, nil, fmt.Errorf("unknown theme %s", v)
			}
		}
		if len(hashes) > 0 && hashes[def.ID] != "" {
			return nil, nil, fmt.Errorf("duplicate scenario %s", def.ID)
		}
		hashes[def.ID] = hash(raw)
		result = append(result, def)
	}
	slices.SortFunc(result, func(a, b scenario) int { return strings.Compare(a.ID, b.ID) })
	return result, hashes, nil
}

func checkJS(page *rod.Page, code string, description string) error {
	result, err := page.Eval(code)
	if err != nil {
		return fmt.Errorf("%s: %w", description, err)
	}
	if !result.Value.Bool() {
		return fmt.Errorf("assertion failed: %s", description)
	}
	return nil
}
func waitJS(page *rod.Page, code string) error { return page.Wait(rod.Eval(code)) }
func step(page *rod.Page, def scenario) (err error) {
	// Rod's MustInput/MustClick use real browser input events. Convert action failures to scenario errors.
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("browser action: %v", failure)
		}
	}()
	if err := waitJS(page, `() => document.body.dataset.view === 'start'`); err != nil {
		return err
	}
	switch def.ID {
	case "chat.start.empty":
		return checkJS(page, `() => document.querySelector('#content').value === '' &&
          !document.querySelector('#content').disabled &&
          !document.querySelector('#quick').hidden && !document.querySelector('#thoughtful').hidden`, "empty start composer with both send modes")
	case "chat.reply.streaming":
		page.MustElement("#content").MustInput("Was ist Marmelade?")
		page.MustElement("#thoughtful").MustClick()
		if err := waitJS(page, `() => document.body.dataset.view === 'conversation' &&
          !!document.querySelector('.response-loader')`); err != nil {
			return err
		}
		return checkJS(page, `() => document.querySelectorAll('.message').length === 2 &&
          document.querySelector('.response-status')?.textContent === 'Warte auf das Modell …' &&
          document.querySelector('.response-loader').getAttribute('aria-label') === 'Walter bereitet die Antwort vor.' &&
          !document.querySelector('#stop').hidden`, "real pending response and stop button")
	case "chat.history.populated":
		page.MustElement("#history-link").MustClick()
		if err := waitJS(page, `() => document.body.dataset.view === 'history' &&
          document.querySelectorAll('#chats li').length === 2`); err != nil {
			return err
		}
		return checkJS(page, `() => document.querySelector('#chats').textContent.includes('Marmelade') &&
          document.querySelector('#chats').textContent.includes('Tabellen')`, "two seeded conversations in history")
	default:
		return fmt.Errorf("unknown scenario %s", def.ID)
	}
}

func browserBinary() (string, error) {
	if path := os.Getenv("WALTER_BROWSER_EXECUTABLE"); path != "" {
		return path, nil
	}
	if path, ok := launcher.LookPath(); ok {
		return path, nil
	}
	return "", errors.New("Chromium not found; set WALTER_BROWSER_EXECUTABLE (browser downloads are disabled)")
}
func newBrowser() (*rod.Browser, func(), error) {
	binary, err := browserBinary()
	if err != nil {
		return nil, nil, err
	}
	launch := launcher.New().Bin(binary).Headless(true).NoSandbox(os.Getenv("WALTER_BROWSER_NO_SANDBOX") == "1").Set("disable-dev-shm-usage")
	url, err := launch.Launch()
	if err != nil {
		return nil, nil, err
	}
	browser := rod.New().ControlURL(url).NoDefaultDevice()
	if err := browser.Connect(); err != nil {
		launch.Kill()
		return nil, nil, err
	}
	return browser, func() { _ = browser.Close(); launch.Kill() }, nil
}
func captureVariant(browser *rod.Browser, def scenario, viewportName, theme, webRoot, output, definitionHash string) (captureEntry, error) {
	d := viewports[viewportName]
	entry := captureEntry{ID: def.ID, Checkpoint: def.Checkpoints[0], Viewport: viewportName, Theme: theme,
		Width: d.width, Height: d.height, DefinitionHash: definitionHash,
		File: strings.Join([]string{def.ID, def.Checkpoints[0], viewportName, theme}, "--") + ".png"}
	server, err := fixtureServer(def.Fixture, webRoot)
	if err != nil {
		return entry, err
	}
	defer server.Close()
	ctx, err := browser.Incognito()
	if err != nil {
		return entry, err
	}
	defer ctx.Close()
	page, err := ctx.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return entry, err
	}
	defer page.Close()
	page = page.Timeout(12_000_000_000) // deadline covers browser readiness, not a fixed UI delay
	if err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{Width: d.width, Height: d.height, DeviceScaleFactor: 1, Mobile: false}); err != nil {
		return entry, err
	}
	if err := (proto.EmulationSetEmulatedMedia{Features: []*proto.EmulationMediaFeature{
		{Name: "prefers-color-scheme", Value: theme}, {Name: "prefers-reduced-motion", Value: "reduce"},
	}}).Call(page); err != nil {
		return entry, err
	}
	if err := (proto.EmulationSetLocaleOverride{Locale: "de-DE"}).Call(page); err != nil {
		return entry, err
	}
	if err := (proto.EmulationSetTimezoneOverride{TimezoneID: "Europe/Berlin"}).Call(page); err != nil {
		return entry, err
	}
	if _, err := page.EvalOnNewDocument(`Math.random = () => 0.25;`); err != nil {
		return entry, err
	}
	if err := page.Navigate(server.URL); err != nil {
		return entry, err
	}
	if err := step(page, def); err != nil {
		return entry, err
	}
	if err := checkJS(page, `() => document.documentElement.scrollWidth <= innerWidth &&
      !!document.querySelector('#error')?.hidden`, "no overflow or UI error"); err != nil {
		return entry, err
	}
	if _, err := page.Eval(`() => document.fonts.ready.then(() => true)`); err != nil {
		return entry, err
	}
	// Remove the cursor without changing content; reduced-motion media is already emulated.
	if _, err := page.Eval(`() => { document.activeElement?.blur(); return true }`); err != nil {
		return entry, err
	}
	if output != "" {
		png, err := page.Screenshot(true, nil)
		if err != nil {
			return entry, err
		}
		if err := os.WriteFile(filepath.Join(output, entry.File), png, 0o644); err != nil {
			return entry, err
		}
	}
	return entry, nil
}

func capture(args []string) error {
	flags := flag.NewFlagSet("capture", flag.ContinueOnError)
	out := flags.String("out", "", "screenshot directory (optional)")
	webRoot := flags.String("web-root", "", "checked-out web assets")
	revision := flags.String("revision", "", "commit revision")
	baseline := flags.String("baseline-contracts", "", "only scenarios matching prior definitions")
	selected := flags.String("scenario", "", "scenario ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", flags.Args())
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return err
	}
	if *webRoot == "" {
		*webRoot = filepath.Join(repoRoot, "web")
	}
	defs, hashes, err := loadDefinitions(definitionDir(repoRoot))
	if err != nil {
		return err
	}
	if len(defs) != len(knownScenarios) {
		return fmt.Errorf("expected %d implemented scenarios; found %d", len(knownScenarios), len(defs))
	}
	for id := range knownScenarios {
		if hashes[id] == "" {
			return fmt.Errorf("missing scenario %s", id)
		}
	}
	if *selected != "" && hashes[*selected] == "" {
		return fmt.Errorf("unknown scenario %s", *selected)
	}
	if *revision == "" {
		value, err := exec.Command("git", "-C", repoRoot, "rev-parse", "HEAD").Output()
		if err != nil {
			return fmt.Errorf("git revision: %w", err)
		}
		*revision = strings.TrimSpace(string(value))
	}
	fixtureSource, err := os.ReadFile("fixture.go")
	if err != nil {
		return err
	}
	m := manifest{Format: 1, Revision: *revision, FixtureSourceHash: hash(fixtureSource), Platform: runtime.GOOS,
		Locale: "de-DE", Timezone: "Europe/Berlin", DeviceScaleFactor: 1, ReducedMotion: "reduce", Images: []captureEntry{}}
	if *baseline != "" {
		_, previous, err := loadDefinitions(*baseline)
		if err != nil {
			return err
		}
		priorFixture, err := os.ReadFile(filepath.Join(*baseline, "..", "browser", "fixture.go"))
		if err == nil && hash(priorFixture) == m.FixtureSourceHash {
			defs = slices.DeleteFunc(defs, func(def scenario) bool { return hashes[def.ID] != previous[def.ID] })
		} else if errors.Is(err, os.ErrNotExist) || err == nil {
			defs = nil // no comparable fixture contract
		} else {
			return err
		}
	}
	if *selected != "" {
		defs = slices.DeleteFunc(defs, func(def scenario) bool { return def.ID != *selected })
	}
	if *out != "" {
		if err := os.MkdirAll(*out, 0o755); err != nil {
			return err
		}
	}
	if len(defs) > 0 {
		browser, cleanup, err := newBrowser()
		if err != nil {
			return err
		}
		defer cleanup()
		info, err := browser.Version()
		if err != nil {
			return err
		}
		m.Chromium = info.Product
		for _, def := range defs {
			for _, viewport := range def.Viewports {
				for _, theme := range def.Themes {
					entry, err := captureVariant(browser, def, viewport, theme, *webRoot, *out, hashes[def.ID])
					if err != nil {
						return fmt.Errorf("%s/%s/%s: %w", def.ID, viewport, theme, err)
					}
					m.Images = append(m.Images, entry)
					fmt.Println("PASS", entry.File)
				}
			}
		}
	}
	if *out != "" {
		if err := saveJSON(filepath.Join(*out, "manifest.json"), m); err != nil {
			return err
		}
	}
	fmt.Printf("Passed %d browser captures.\n", len(m.Images))
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
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
