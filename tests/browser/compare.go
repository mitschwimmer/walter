package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type comparison struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	Before        string `json:"beforeFile,omitempty"`
	After         string `json:"afterFile,omitempty"`
	Difference    string `json:"differenceFile,omitempty"`
	ChangedPixels int    `json:"changedPixels,omitempty"`
	TotalPixels   int    `json:"totalPixels,omitempty"`
}
type comparisonSummary struct {
	BaseRevision      string         `json:"baseRevision"`
	CandidateRevision string         `json:"candidateRevision"`
	Browser           string         `json:"browser"`
	Counts            map[string]int `json:"counts"`
	Entries           []comparison   `json:"entries"`
}

func fileCopy(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
func imageEntries(m manifest) (map[string]captureEntry, error) {
	result := make(map[string]captureEntry)
	for _, entry := range m.Images {
		expected := strings.Join([]string{entry.ID, entry.Checkpoint, entry.Viewport, entry.Theme}, "--") + ".png"
		if entry.File != expected || filepath.Base(entry.File) != entry.File {
			return nil, fmt.Errorf("unsafe screenshot name %q", entry.File)
		}
		if _, exists := result[entry.File]; exists {
			return nil, fmt.Errorf("duplicate screenshot %s", entry.File)
		}
		result[entry.File] = entry
	}
	return result, nil
}

// Compare decoded pixels, not PNG byte serialization or metadata. Produce a
// real pixel-difference image only when the capture dimensions match.
func pixelDifference(base, candidate []byte) (bool, *image.RGBA, int, int, error) {
	first, err := png.Decode(bytes.NewReader(base))
	if err != nil {
		return false, nil, 0, 0, err
	}
	second, err := png.Decode(bytes.NewReader(candidate))
	if err != nil {
		return false, nil, 0, 0, err
	}
	if first.Bounds().Dx() != second.Bounds().Dx() || first.Bounds().Dy() != second.Bounds().Dy() {
		return false, nil, 0, 0, nil
	}
	bounds := image.Rect(0, 0, first.Bounds().Dx(), first.Bounds().Dy())
	output := image.NewRGBA(bounds)
	changed := 0
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			a := first.At(first.Bounds().Min.X+x, first.Bounds().Min.Y+y)
			b := second.At(second.Bounds().Min.X+x, second.Bounds().Min.Y+y)
			if a != b && color.RGBAModel.Convert(a) != color.RGBAModel.Convert(b) {
				changed++
				output.SetRGBA(x, y, color.RGBA{R: 236, G: 67, B: 70, A: 255})
				continue
			}
			c := color.RGBAModel.Convert(b).(color.RGBA)
			gray := uint8((uint16(c.R) + uint16(c.G) + uint16(c.B)) / 3)
			output.SetRGBA(x, y, color.RGBA{R: gray / 2, G: gray / 2, B: gray / 2, A: 255})
		}
	}
	return true, output, changed, bounds.Dx() * bounds.Dy(), nil
}

func compare(args []string) error {
	flags := flag.NewFlagSet("compare", flag.ContinueOnError)
	base := flags.String("base", "", "main capture directory")
	candidate := flags.String("candidate", "", "PR capture directory")
	baseDefs := flags.String("base-defs", "", "main scenario manifest directory")
	candidateDefs := flags.String("candidate-defs", "", "PR scenario manifest directory")
	output := flags.String("out", "", "report directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", flags.Args())
	}
	if *base == "" || *candidate == "" || *baseDefs == "" || *candidateDefs == "" || *output == "" {
		return errors.New("--base, --candidate, --base-defs, --candidate-defs and --out are required")
	}
	b, err := readManifest(*base)
	if err != nil {
		return err
	}
	c, err := readManifest(*candidate)
	if err != nil {
		return err
	}
	if b.Format != 1 || c.Format != 1 {
		return errors.New("unsupported capture manifest")
	}
	if len(b.Images) != 0 && (b.Chromium != c.Chromium || b.Platform != c.Platform || b.Locale != c.Locale || b.Timezone != c.Timezone || b.DeviceScaleFactor != c.DeviceScaleFactor || b.ReducedMotion != c.ReducedMotion) {
		return errors.New("browser capture environments differ; cannot compare screenshots")
	}
	_, baseHashes, err := loadDefinitions(*baseDefs)
	if err != nil {
		return err
	}
	_, candidateHashes, err := loadDefinitions(*candidateDefs)
	if err != nil {
		return err
	}
	baseFixture, err := os.ReadFile(filepath.Join(*baseDefs, "..", "browser", "fixture.go"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	candidateFixture, err := os.ReadFile(filepath.Join(*candidateDefs, "..", "browser", "fixture.go"))
	if err != nil {
		return err
	}
	changedFixture := baseFixture != nil && !bytes.Equal(baseFixture, candidateFixture)
	bm, err := imageEntries(b)
	if err != nil {
		return err
	}
	cm, err := imageEntries(c)
	if err != nil {
		return err
	}
	counts := map[string]int{"unchanged": 0, "changed": 0, "added": 0, "removed": 0, "contract-changed": 0}
	summary := comparisonSummary{BaseRevision: b.Revision, CandidateRevision: c.Revision,
		Browser: c.Chromium, Counts: counts, Entries: []comparison{}}
	for _, path := range []string{"images/main", "images/candidate", "images/diff"} {
		if err := os.MkdirAll(filepath.Join(*output, path), 0o755); err != nil {
			return err
		}
	}
	for _, entry := range c.Images {
		_, known := baseHashes[entry.ID]
		status := "unchanged"
		if !known {
			status = "added"
		} else if baseHashes[entry.ID] != candidateHashes[entry.ID] || changedFixture {
			status = "contract-changed"
		}
		row := comparison{Name: entry.File, Status: status}
		currentBytes, err := os.ReadFile(filepath.Join(*candidate, entry.File))
		if err != nil {
			return err
		}
		_, _, err = png.Decode(bytes.NewReader(currentBytes))
		if err != nil {
			return fmt.Errorf("invalid screenshot %s: %w", entry.File, err)
		}
		if status == "unchanged" {
			_, ok := bm[entry.File]
			if !ok {
				return fmt.Errorf("baseline lacks %s despite unchanged scenario contract", entry.File)
			}
			baseBytes, err := os.ReadFile(filepath.Join(*base, entry.File))
			if err != nil {
				return err
			}
			match, delta, changed, total, err := pixelDifference(baseBytes, currentBytes)
			if err != nil {
				return err
			}
			if !match || changed > 0 {
				row.Status = "changed"
				row.ChangedPixels, row.TotalPixels = changed, total
				row.Before = "images/main/" + entry.File
				if err := fileCopy(filepath.Join(*base, entry.File), filepath.Join(*output, row.Before)); err != nil {
					return err
				}
				if match {
					row.Difference = "images/diff/" + entry.File
					dst, err := os.Create(filepath.Join(*output, row.Difference))
					if err != nil {
						return err
					}
					encErr := png.Encode(dst, delta)
					closeErr := dst.Close()
					if encErr != nil {
						return encErr
					}
					if closeErr != nil {
						return closeErr
					}
				}
			}
		}
		counts[row.Status]++
		if row.Status == "unchanged" {
			continue
		}
		row.After = "images/candidate/" + entry.File
		if err := fileCopy(filepath.Join(*candidate, entry.File), filepath.Join(*output, row.After)); err != nil {
			return err
		}
		summary.Entries = append(summary.Entries, row)
	}
	for _, entry := range b.Images {
		if _, defined := candidateHashes[entry.ID]; !defined {
			continue
		}
		if baseHashes[entry.ID] != candidateHashes[entry.ID] || changedFixture {
			continue
		}
		if _, ok := cm[entry.File]; !ok {
			return fmt.Errorf("candidate is missing required image %s", entry.File)
		}
	}
	var retired []string
	for id := range baseHashes {
		if candidateHashes[id] == "" {
			retired = append(retired, id)
		}
	}
	sort.Strings(retired)
	for _, id := range retired {
		counts["removed"]++
		summary.Entries = append(summary.Entries, comparison{Name: id, Status: "removed"})
	}
	if err := saveJSON(filepath.Join(*output, "summary.json"), summary); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*output, "index.html"), []byte(gallery(summary)), 0o644); err != nil {
		return err
	}
	if file := os.Getenv("GITHUB_STEP_SUMMARY"); file != "" {
		description := fmt.Sprintf("### Rod UI visual scenarios\n\n| Unchanged | Changed | Added | Removed | Contract changed |\n|---:|---:|---:|---:|---:|\n|%d|%d|%d|%d|%d|\n\nThe changed-only gallery is in the `walter-rod-visual-scenarios` artifact.\n", counts["unchanged"], counts["changed"], counts["added"], counts["removed"], counts["contract-changed"])
		f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, err = f.WriteString(description)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	fmt.Printf("Visual comparison: %v\n", counts)
	return nil
}

func gallery(summary comparisonSummary) string {
	var out strings.Builder
	out.WriteString(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Walter Rod visual review</title><style>
body{margin:2rem auto;max-width:1100px;padding:0 1rem;font:16px system-ui;background:#171d1a;color:#f7f5ee}small,figcaption{color:#abb8ae}section{border-top:1px solid #526157;padding:1rem 0 2rem}.images{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,300px),1fr));gap:1rem}figure{margin:0;min-width:0}img{display:block;max-width:100%;height:auto;border:1px solid #526157}figcaption{padding:.5rem 0}
</style><main><h1>Walter visual review (Rod)</h1><p>main `)
	out.WriteString(html.EscapeString(summary.BaseRevision))
	out.WriteString(" → candidate ")
	out.WriteString(html.EscapeString(summary.CandidateRevision))
	out.WriteString("</p><p>")
	for _, status := range []string{"unchanged", "changed", "added", "removed", "contract-changed"} {
		fmt.Fprintf(&out, "%s: %d · ", status, summary.Counts[status])
	}
	out.WriteString("</p><p>Showing only changed and new views, removed scenarios, and contract changes.</p>")
	if len(summary.Entries) == 0 {
		out.WriteString("<p>No visual changes.</p>")
	}
	for _, entry := range summary.Entries {
		fmt.Fprintf(&out, "<section><h2>%s <small>%s</small></h2><div class=\"images\">", html.EscapeString(entry.Name), html.EscapeString(entry.Status))
		for _, pair := range [][2]string{{"main", entry.Before}, {"candidate", entry.After}, {"difference", entry.Difference}} {
			if pair[1] == "" {
				continue
			}
			fmt.Fprintf(&out, "<figure><img loading=\"lazy\" src=\"%s\" alt=\"%s\"><figcaption>%s</figcaption></figure>", html.EscapeString(pair[1]), pair[0], pair[0])
		}
		out.WriteString("</div></section>")
	}
	out.WriteString("</main></html>\n")
	return out.String()
}
