package main

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// This is the native replacement for tests/ui.test.mjs. It uses exactly the
// checked-out frontend and a synthetic, stateful HTTP server (no auth bypass).
func TestBrowserInteractions(t *testing.T) {
	bin := os.Getenv("WALTER_BROWSER_EXECUTABLE")
	if bin == "" {
		if path, ok := launcher.LookPath(); ok {
			bin = path
		}
	}
	if bin == "" {
		t.Skip("Chromium required for browser regressions")
	}
	for _, theme := range []string{"light", "dark"} {
		t.Run(theme, func(t *testing.T) { checkBrowserInteractions(t, bin, theme) })
	}
}

func checked(t *testing.T, page *rod.Page, js, description string) {
	t.Helper()
	if err := assertPage(page, js, description); err != nil {
		t.Fatal(err)
	}
}
func ready(t *testing.T, page *rod.Page, js string) {
	t.Helper()
	if err := waitFor(page, js); err != nil {
		t.Fatal("UI readiness: ", err)
	}
}
func fill(page *rod.Page, value string) {
	page.MustElement("#content").MustSelectAllText().MustInput(value)
}
func noOverflow(t *testing.T, page *rod.Page) {
	t.Helper()
	checked(t, page, `() => document.documentElement.scrollWidth <= innerWidth`, "viewport must not overflow")
}
func setViewport(t *testing.T, page *rod.Page, width, height int) {
	t.Helper()
	if err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{Width: width, Height: height, DeviceScaleFactor: 1, Mobile: false}); err != nil {
		t.Fatal(err)
	}
	noOverflow(t, page)
}
func idle(t *testing.T, page *rod.Page) {
	t.Helper()
	ready(t, page, `() => document.querySelector('#composer')?.getAttribute('aria-busy') === 'false' && !document.querySelector('#content').disabled`)
}
func visible(selector string) string {
	return fmt.Sprintf(`() => {const e=document.querySelector(%q);return !!e && !e.hidden && !!e.getClientRects().length}`, selector)
}
func hidden(selector string) string {
	return fmt.Sprintf(`() => {const e=document.querySelector(%q);return !e || e.hidden || !e.getClientRects().length}`, selector)
}
func contentEqual(selector, value string) string {
	return fmt.Sprintf(`() => document.querySelector(%q)?.textContent === %q`, selector, value)
}
func countEqual(selector string, n int) string {
	return fmt.Sprintf(`() => document.querySelectorAll(%q).length === %d`, selector, n)
}
func view(name string) string { return fmt.Sprintf(`() => document.body.dataset.view === %q`, name) }
func fieldValue(value string) string {
	return fmt.Sprintf(`() => document.querySelector('#content')?.value === %q`, value)
}
func loginLayout(t *testing.T, page *rod.Page, size string, gap int) {
	t.Helper()
	checked(t, page, fmt.Sprintf(`() => {
      const title=document.querySelector('.headline').getBoundingClientRect();
      const panel=document.querySelector('#login').getBoundingClientRect();
      const text=getComputedStyle(document.querySelector('#login p'));
      return Math.abs(title.left+title.width/2-innerWidth/2)<2 &&
        Math.abs(panel.left+panel.width/2-innerWidth/2)<2 &&
        panel.top-title.bottom>=%d && text.fontSize===%q &&
        text.textAlign==='start' && text.fontFamily.includes('Arial') &&
        parseFloat(getComputedStyle(document.querySelector('#login')).borderRadius)>=16;
    }`, gap, size), "login layout and readable typography")
}
func themeMedia(t *testing.T, page *rod.Page, theme string) {
	t.Helper()
	emulated := proto.EmulationSetEmulatedMedia{Features: []*proto.EmulationMediaFeature{
		{Name: "prefers-color-scheme", Value: theme}, {Name: "prefers-reduced-motion", Value: "no-preference"},
	}}
	if err := emulated.Call(page); err != nil {
		t.Fatal(err)
	}
}
func checkBrowserInteractions(t *testing.T, bin, theme string) {
	f := newRegressionFixture(t)
	defer f.close()
	l := launcher.New().Bin(bin).Headless(true).NoSandbox(os.Getenv("WALTER_BROWSER_NO_SANDBOX") == "1").Set("disable-dev-shm-usage")
	url, err := l.Launch()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Kill()
	browser := rod.New().ControlURL(url).NoDefaultDevice()
	if err := browser.Connect(); err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	page = page.Timeout(15 * time.Second)
	setViewport(t, page, 1071, 1579)
	themeMedia(t, page, theme)
	if err := (proto.EmulationSetLocaleOverride{Locale: "de-DE"}).Call(page); err != nil {
		t.Fatal(err)
	}
	if err := (proto.EmulationSetTimezoneOverride{TimezoneID: "Europe/Berlin"}).Call(page); err != nil {
		t.Fatal(err)
	}
	if _, err := page.EvalOnNewDocument(`Math.random=()=>0.25;`); err != nil {
		t.Fatal(err)
	}
	if err := page.Navigate(f.url()); err != nil {
		t.Fatal(err)
	}

	// Login request held: authentic loading view until the fixture releases it.
	ready(t, page, view("loading"))
	checked(t, page, hidden("#login"), "login hidden during session loading")
	close(f.sessionHold)
	ready(t, page, view("start"))
	ready(t, page, `() => document.activeElement?.id==='content'`)
	checked(t, page, contentEqual("#revision", "Build 10d95b1"), "build revision footer")
	checked(t, page, `() => !document.querySelector('aside')`, "no obsolete sidebar")
	checked(t, page, hidden("#context-info"), "no context on welcome screen")
	// Starter questions populate the composer without sending or selecting a mode.
	ready(t, page, `() => !document.querySelector('.idea').disabled`)
	page.MustElement(".idea").MustClick()
	checked(t, page, fieldValue("Warum ist der Himmel blau?"), "starter populates prompt")
	checked(t, page, `() => [...document.querySelectorAll('.idea')].every(b=>b.disabled)`, "starters cannot overwrite a draft")
	if n, subs, _ := f.stats(); n != 0 || len(subs) != 0 {
		t.Fatal("starter unexpectedly sent a message")
	}
	// The rounded composer itself owns the input focus indicator.
	fill(page, "Was ist Marmelade?")
	if _, err := page.Eval(`() => {document.querySelector('#quick').focus(); document.querySelector('#content').focus()}`); err != nil {
		t.Fatal(err)
	}
	focusColor := "rgb(49, 89, 179)"
	if theme == "dark" {
		focusColor = "rgb(197, 214, 255)"
	}
	checked(t, page, `() => document.querySelector('#content').validity.valid`, "valid prompt for focus test")
	ready(t, page, fmt.Sprintf(`() => {
      const style=getComputedStyle(document.querySelector('#composer'));
      return style.outlineStyle==='solid' && style.borderTopColor===%q;
    }`, focusColor))
	checked(t, page, `() => getComputedStyle(document.querySelector('#content')).outlineStyle==='none'`, "composer focus without duplicate textarea outline")
	checked(t, page, `() => {
       const headline=document.querySelector('.headline').getBoundingClientRect();
       const composer=document.querySelector('#composer').getBoundingClientRect();
       const style=getComputedStyle(document.querySelector('.headline'));
       return Math.abs(headline.left+headline.width/2-innerWidth/2)<2 &&
         Math.abs(composer.left+composer.width/2-innerWidth/2)<2 &&
         composer.top-headline.bottom>=48 && style.textAlign==='center' && style.fontWeight==='750' &&
         parseFloat(getComputedStyle(document.querySelector('#composer')).borderRadius)>=20 &&
         style.backgroundImage.includes('headline-overlay.svg')
    }`, "welcome headline and composer layout")

	// Hold the first assistant reply. Every progress phase must be visible.
	holdAnswer := f.holdNextAnswer()
	page.MustElement("#thoughtful").MustClick()
	ready(t, page, view("conversation"))
	ready(t, page, visible(".response-loader"))
	checked(t, page, contentEqual(".response-status", "Warte auf das Modell …"), "pending reply status")
	checked(t, page, hidden("#content"), "composer hidden during inference")
	checked(t, page, visible("#stop"), "stop available during inference")
	checked(t, page, countEqual(".message", 2), "first user/assistant pair")
	checked(t, page, `() => document.querySelector('.response-loader')?.getAttribute('aria-label')==='Walter bereitet die Antwort vor.'`, "accessible loader")
	for _, p := range []struct {
		data any
		text string
	}{
		{map[string]any{"phase": "prefill", "percent": 23}, "Prefill: 23%"},
		{map[string]any{"phase": "prefill", "percent": 87}, "Prefill: 87%"},
		{map[string]any{"phase": "thinking"}, "Denkt nach …"},
		{map[string]any{"phase": "thinking", "tokens": 123}, "Denkt nach … · 123 Tokens erzeugt"},
	} {
		select {
		case f.progress <- p.data:
		case <-time.After(3 * time.Second):
			t.Fatal("progress fixture blocked")
		}
		ready(t, page, contentEqual(".response-status", p.text))
	}
	checked(t, page, `() => getComputedStyle(document.querySelector('.response-loader'),'::before').animationName==='loader-draw, loader-shift'`, "animated loader")
	setViewport(t, page, 320, 844)
	themeMedia(t, page, theme)
	noOverflow(t, page)
	setViewport(t, page, 1071, 1579)
	holdReload := f.holdNextReload()
	close(holdAnswer)
	ready(t, page, visible(".assistant p"))
	ready(t, page, hidden(".response-loader"))
	checked(t, page, visible("#stop"), "stop persists through synchronization")
	// Attempting to navigate while persistence is pending must be ignored.
	if _, err := page.Eval(`() => {document.querySelector('#history-link').dispatchEvent(new MouseEvent('click',{bubbles:true,cancelable:true}));return true}`); err != nil {
		t.Fatal(err)
	}
	ready(t, page, view("conversation"))
	close(holdReload)
	f.clearReload()
	idle(t, page)
	creations, submissions, _ := f.stats()
	if creations != 1 || len(submissions) != 1 || submissions[0] != "Was ist Marmelade?" {
		t.Fatalf("unexpected submission state: creations=%d submissions=%v", creations, submissions)
	}
	checked(t, page, contentEqual("#context-info", "Kontext: 50% · 8.192 / 16.384 Tokens"), "visible context usage")
	checked(t, page, visible("#context-info"), "context visible")
	checked(t, page, countEqual(".assistant table", 1), "server-rendered Markdown table")
	checked(t, page, `() => document.querySelector('.assistant')?.tagName==='ARTICLE'`, "semantic assistant article")
	checked(t, page, `() => {
       const a=document.querySelector('.assistant .message-content'); const parent=a.closest('article');
       const code=a.querySelector('pre'); const table=a.querySelector('table');
       return a.getBoundingClientRect().left-parent.getBoundingClientRect().left>=40 &&
         a.getBoundingClientRect().right<=parent.getBoundingClientRect().right &&
         !!parent.querySelector('.speaker img[src="/walter.svg"]') &&
         a.getBoundingClientRect().width<parent.getBoundingClientRect().width &&
         getComputedStyle(a.querySelector('p')).fontFamily.includes('Arial') &&
         getComputedStyle(a.querySelector('p')).textAlign==='start' &&
         getComputedStyle(a.querySelector('h2')).fontWeight==='700' &&
         getComputedStyle(code.querySelector('code')).fontFamily==='monospace' &&
         getComputedStyle(code).whiteSpace==='pre' && code.scrollWidth>code.clientWidth &&
         getComputedStyle(table.querySelector('td')).whiteSpace==='nowrap'
    }`, "narrow markdown article, scrollable code and table")
	checked(t, page, visible("#quick"), "quick mode after response")
	checked(t, page, visible("#thoughtful"), "thoughtful mode after response")
	for _, scheme := range []string{"dark", "light"} {
		themeMedia(t, page, scheme)
		bg := "rgb(247, 248, 251)"
		fg := "rgb(37, 39, 58)"
		code := "rgb(237, 240, 247)"
		dialog := "rgb(255, 255, 255)"
		if scheme == "dark" {
			bg = "rgb(18, 20, 32)"
			fg = "rgb(243, 242, 250)"
			code = "rgb(35, 39, 58)"
			dialog = "rgb(29, 32, 48)"
		}
		checked(t, page, fmt.Sprintf(`() => {
          const style=x=>getComputedStyle(document.querySelector(x));
          return style('body').backgroundColor===%q && style('.assistant').color===%q &&
            style('.assistant pre').backgroundColor===%q &&
            style('dialog').backgroundColor===%q &&
            style('.error').backgroundColor===style('dialog').backgroundColor;
        }`, bg, fg, code, dialog), "light/dark responsive colors")
	}
	// An explicit theme must take precedence over the opposite OS preference.
	for _, tc := range []struct{ scheme, opposite, background string }{
		{"light", "dark", "rgb(247, 248, 251)"},
		{"dark", "light", "rgb(18, 20, 32)"},
	} {
		themeMedia(t, page, tc.opposite)
		page.MustElement(".theme-picker summary").MustClick()
		if _, err := page.Eval(fmt.Sprintf(`() => {
          const select=document.querySelector('#theme');select.value=%q;
          select.dispatchEvent(new Event('change',{bubbles:true}));
        }`, tc.scheme)); err != nil {
			t.Fatal(err)
		}
		checked(t, page, fmt.Sprintf(`() => getComputedStyle(document.body).backgroundColor===%q &&
          getComputedStyle(document.documentElement).colorScheme===%q`, tc.background, tc.scheme), "explicit theme overrides system preference")
		checked(t, page, fmt.Sprintf(`() => localStorage.getItem('walter-theme')===%q`, tc.scheme), "explicit preference stored")
		page.MustElement(".theme-picker summary").MustClick()
		if _, err := page.Eval(`() => {
          const select=document.querySelector('#theme');select.value='system';
          select.dispatchEvent(new Event('change',{bubbles:true}));
        }`); err != nil {
			t.Fatal(err)
		}
	}
	themeMedia(t, page, theme)
	originalURL := page.MustInfo().URL
	fill(page, "<img src=x onerror=alert(1)>")
	page.MustElement("#quick").MustClick()
	idle(t, page)
	checked(t, page, countEqual(".user img", 0), "user prompt must stay plain text")
	checked(t, page, countEqual(".message", 4), "second reply pair")
	if n, _, _ := f.stats(); n != 1 {
		t.Fatalf("followup created a second conversation: %d", n)
	}
	checked(t, page, contentEqual("#context-info", "Kontext: 25% · 4.096 / 16.384 Tokens"), "per-mode context")

	// Both context-unknown and one/both full capacity modes.
	f.setContext(false, 4096, 8192)
	page.MustReload()
	ready(t, page, view("conversation"))
	idle(t, page)
	checked(t, page, contentEqual("#context-info", "Kontext: unbekannt"), "unknown context")
	checked(t, page, `() => !document.querySelector('#quick').disabled && !document.querySelector('#thoughtful').disabled`, "missing context must not block submission")
	f.setContext(true, 16384, 8192)
	page.MustReload()
	ready(t, page, view("conversation"))
	idle(t, page)
	checked(t, page, `() => document.querySelector('#quick').disabled && !document.querySelector('#thoughtful').disabled`, "individual full context disables only its mode")
	f.setContext(true, 16384, 16384)
	page.MustReload()
	ready(t, page, view("conversation"))
	ready(t, page, visible("#context-full"))
	checked(t, page, `() => document.querySelector('#quick').disabled && document.querySelector('#thoughtful').disabled && !document.querySelector('#logout').disabled`, "full context and logout")
	setViewport(t, page, 320, 844)
	page.MustElement("#context-full a").MustClick()
	ready(t, page, view("start"))
	ready(t, page, `() => document.activeElement?.id==='content'`)
	f.setContext(true, 4096, 8192)
	setViewport(t, page, 1071, 1579)
	page.MustNavigate(originalURL)
	ready(t, page, view("conversation"))
	idle(t, page)

	// History links, deep links, interruption, cancellation and deletion.
	page.MustElement("#history-link").MustClick()
	ready(t, page, view("history"))
	ready(t, page, countEqual("#chats a", 1))
	checked(t, page, `() => document.querySelector('#chats time')?.textContent.includes('16. Oktober 2026')`, "localized history timestamp")
	page.MustElement("#chats a").MustClick()
	ready(t, page, view("conversation"))
	page.MustReload()
	ready(t, page, view("conversation"))
	checked(t, page, countEqual(".message", 4), "deep-link reload preserves responses")
	fill(page, "Noch eine Frage")
	page.MustElement("#thoughtful").MustClick()
	ready(t, page, countEqual(".assistant", 3))
	page.MustElement("#stop").MustClick()
	idle(t, page)
	_, submissions, _ = f.stats()
	if len(submissions) != 3 {
		t.Fatalf("stop resent prompt or lost submission: %v", submissions)
	}
	checked(t, page, `() => document.querySelector('.assistant:last-of-type small')?.textContent==='Abgebrochen' ||
        [...document.querySelectorAll('.assistant')].at(-1)?.querySelector('small')?.textContent==='Abgebrochen'`, "aborted response is persisted")
	page.MustElement("#delete").MustClick()
	page.MustElement("#delete-dialog button[value=cancel]").MustClick()
	if _, _, n := f.stats(); n != 1 {
		t.Fatalf("cancel deleted conversation: %d", n)
	}
	page.MustElement("#delete").MustClick()
	page.MustElement("#confirm-delete").MustClick()
	ready(t, page, view("history"))
	ready(t, page, `() => document.querySelector('#history-status')?.textContent.startsWith('Hier findest')`)
	if _, _, n := f.stats(); n != 0 {
		t.Fatalf("delete did not remove conversation: %d", n)
	}
	page.MustElement("#new").MustClick()
	ready(t, page, view("start"))
	f.setRejectNext()
	fill(page, "Frage bleibt erhalten")
	page.MustElement("#quick").MustClick()
	ready(t, page, visible("#draft-hint"))
	checked(t, page, fieldValue("Frage bleibt erhalten"), "failed send retains draft")
	page.MustElement("#quick").MustClick()
	idle(t, page)
	creations, submissions, _ = f.stats()
	if creations != 2 || len(submissions) != 4 {
		t.Fatalf("retry duplicated conversation or submission: creations=%d subs=%v", creations, submissions)
	}
	for _, width := range []int{320, 390, 480, 600, 601, 768, 769} {
		setViewport(t, page, width, 844)
		font := "16px"
		if width > 600 {
			font = "17px"
		}
		checked(t, page, fmt.Sprintf(`() => {
          const article=document.querySelector('.assistant .message-content');
          return !!article && getComputedStyle(article).fontSize===%q;
        }`, font), "responsive reply typography")
		if width <= 480 {
			checked(t, page, `() => getComputedStyle(document.querySelector('#content')).fontSize==='16px'`, "mobile input prevents zoom")
		}
	}
	page.MustElement("#new").MustClick()
	ready(t, page, view("start"))
	for _, width := range []int{320, 390} {
		setViewport(t, page, width, 844)
	}
	page.MustElement("#history-link").MustClick()
	ready(t, page, view("history"))
	retained := f.lastConversation().ID
	removable := f.createHistoryItem("Nur dieses Gespräch")
	page.MustElement("#history-link").MustClick()
	ready(t, page, countEqual("#chats li", 2))
	page.MustElement(`button[aria-label="Löschen: Nur dieses Gespräch"]`).MustClick()
	page.MustElement("#delete-dialog button[value=cancel]").MustClick()
	if _, _, n := f.stats(); n != 2 {
		t.Fatalf("history cancellation deleted item: %d", n)
	}
	f.setRejectDelete()
	page.MustElement(`button[aria-label="Löschen: Nur dieses Gespräch"]`).MustClick()
	page.MustElement("#confirm-delete").MustClick()
	ready(t, page, visible("#error"))
	if _, _, n := f.stats(); n != 2 {
		t.Fatalf("failed delete removed item: %d", n)
	}
	page.MustElement("#dismiss-error").MustClick()
	page.MustElement(`button[aria-label="Löschen: Nur dieses Gespräch"]`).MustClick()
	page.MustElement("#confirm-delete").MustClick()
	ready(t, page, countEqual("#chats li", 1))
	f.mu.Lock()
	_, keep := f.conversations[retained]
	_, gone := f.conversations[removable]
	f.mu.Unlock()
	if !keep || gone {
		t.Fatal("deletion removed wrong history entry")
	}
	checked(t, page, `() => location.hash==='#historie'`, "delete keeps history route")
	page.MustElement("#chats button").MustClick()
	page.MustElement("#confirm-delete").MustClick()
	ready(t, page, `() => document.querySelector('#history-status')?.textContent.startsWith('Hier findest')`)
	page.MustElement("#history .text-link").MustClick()
	ready(t, page, view("start"))
	page.MustElement("#home").MustClick()
	ready(t, page, view("start"))

	// Expiration and explicit logout both clear private data and recover login.
	page.MustElement("#history-link").MustClick()
	ready(t, page, view("history"))
	f.setExpire()
	page.MustElement("#history-link").MustClick()
	ready(t, page, view("login"))
	checked(t, page, hidden("#account-menu"), "session expiry hides account menu")
	checked(t, page, countEqual(".message", 0), "expiry clears conversation")
	for _, width := range []int{320, 390} {
		setViewport(t, page, width, 844)
		loginLayout(t, page, "17px", 36)
	}
	f.setSignedIn(true)
	page.MustNavigate(f.url())
	ready(t, page, view("start"))
	page.MustElement("#logout").MustClick()
	ready(t, page, view("login"))
	f.mu.Lock()
	signed := f.signedIn
	f.mu.Unlock()
	if signed {
		t.Fatal("logout did not end session")
	}
	setViewport(t, page, 1071, 1579)
	loginLayout(t, page, "19px", 64)
	f.setFooterLinks(false)
	page.MustReload()
	ready(t, page, view("login"))
	checked(t, page, contentEqual("#revision", "Build 10d95b1"), "build revision without footer links")
	checked(t, page, visible("#footer"), "build-only footer")
	checked(t, page, hidden("#imprint"), "no absent imprint URL")
	checked(t, page, hidden("#privacy"), "no absent privacy URL")

	// Pressing Enter alone never sends. Explicit quick/thoughtful buttons do.
	f.setSignedIn(true)
	page.MustNavigate(f.url())
	ready(t, page, view("start"))
	_, before, _ := f.stats()
	fill(page, "Erste Zeile")
	page.Keyboard.MustType(input.Enter)
	page.MustInsertText("Zweite Zeile")
	checked(t, page, fieldValue("Erste Zeile\nZweite Zeile"), "Enter creates a newline in textarea")
	_, submissions, _ = f.stats()
	_ = before
	if len(submissions) != 4 {
		t.Fatalf("keyboard unexpectedly submitted: %v", submissions)
	}
	page.MustElement("#quick").MustClick()
	idle(t, page)
	_, submissions, _ = f.stats()
	if len(submissions) != 5 || submissions[len(submissions)-1] != "Erste Zeile\nZweite Zeile" {
		t.Fatalf("explicit quick submission lost newline: %v", submissions)
	}
	fill(page, "Bitte bedacht antworten")
	page.MustElement("#thoughtful").MustClick()
	idle(t, page)
	if c := f.lastConversation(); c.Thinking != "thoughtful" {
		t.Fatalf("mode not thoughtful: %#v", c)
	}
	fill(page, "Weitere Frage")
	page.Keyboard.MustType(input.Enter)
	_, submissions, _ = f.stats()
	if len(submissions) != 6 {
		t.Fatalf("Enter submitted without mode: %v", submissions)
	}
	page.MustElement("#quick").MustClick()
	idle(t, page)
	if c := f.lastConversation(); c.Thinking != "quick" {
		t.Fatalf("mode not quick: %#v", c)
	}
	checked(t, page, hidden("#error"), "no uncaught UI error")
	if errs := f.errors(); len(errs) > 0 {
		t.Fatalf("fixture errors: %v", errs)
	}
	t.Log("Go+Rod UI passed: start, streaming/progress, markdown, modes, context, stop/retry, history, delete, expiry/logout, keyboard and responsive states")
}
