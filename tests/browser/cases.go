package main

import (
	"context"
	"fmt"
	"time"

	"github.com/archaic-work/visual-test/visualtest/rod"
	browser "github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
)

type check func(*browser.Page) error

func wait(p *browser.Page, js string) error { return p.Wait(browser.Eval(js)) }
func state(p *browser.Page, name string) error {
	return p.Wait(browser.Eval("name => document.body.dataset.view === name",name))
}
func visible(p *browser.Page, selector string) error {
	return p.Wait(browser.Eval("s => { const e=document.querySelector(s); return !!e && !!e.getClientRects().length }",selector))
}
func idle(p *browser.Page) error {
	return wait(p,"() => document.querySelector('#composer').getAttribute('aria-busy') === 'false' && !document.querySelector('#content').disabled")
}
func checks(p *browser.Page, tests ...string) error {
	for _, test:=range tests { if err:=wait(p,test);err!=nil { return fmt.Errorf("assert %s: %w",test,err) } }
	return nil
}
func click(p *browser.Page, selector string) { p.MustElement(selector).MustClick() }
func fill(p *browser.Page, value string) { p.MustElement("#content").MustSelectAllText().MustInput(value) }
func capture(p *browser.Page, shot func(string) error, checkpoint string) error {
	if err:=visible(p,"#revision");err!=nil{return err}
	if err:=wait(p,"() => document.documentElement.scrollWidth <= innerWidth");err!=nil{return err}
	// Let finite CSS transitions settle, including native dialogs.
	if _,err:=p.Eval("() => Promise.allSettled(document.getAnimations().filter(a => a.effect.getTiming().iterations !== Infinity).map(a => a.finished)).then(() => true)");err!=nil{return err}
	return shot(checkpoint)
}
func complete(p *browser.Page) error {
	if err:=state(p,"conversation");err!=nil{return err}
	return checks(p,
		"() => document.querySelectorAll('.message').length === 2",
		"() => document.querySelector('.assistant').tagName === 'ARTICLE'",
		"() => !!document.querySelector('.assistant table') && !!document.querySelector('.assistant pre')",
		"() => document.querySelector('.user .message-content').textContent === 'Was ist Marmelade?'",
		"() => document.querySelector('#context-info').textContent === 'Kontext: 50% · 8.192 / 16.384 Tokens'",
		"() => getComputedStyle(document.querySelector('.assistant pre')).whiteSpace === 'pre'",
		"() => getComputedStyle(document.querySelector('.assistant td')).whiteSpace === 'nowrap'",
		"() => getComputedStyle(document.querySelector('.assistant h2')).fontWeight === '500'",
		"() => getComputedStyle(document.querySelector('.assistant p')).fontFamily === 'sans-serif'")
}
func streamCommand(ctx context.Context, f *fixture, command string) error {
	select { case f.stream<-command:return nil;case <-ctx.Done():return ctx.Err();case <-time.After(5*time.Second):return fmt.Errorf("fixture stream blocked") }
}
func cases() map[string]rod.Case {
	result:=map[string]rod.Case{}
	add:=func(id,fixtureName,route string,run func(context.Context,*browser.Page,*fixture,func(string)error)error) {
		var f *fixture
		result[id]=rod.Case{Fixture:fixtureName,
			ContractFiles:[]string{"tests/browser/cases.go","tests/browser/fixture.go","tests/browser/sse.go","tests/browser/main.go","tests/browser/go.mod"},
			InitScript:"Math.random = () => 0.25;",
			Serve:func(ctx context.Context,assets string)(string,func(),error){
				f=newFixture(fixtureName);s:=f.serve(assets)
				return s.URL+route,func(){s.CloseClientConnections();s.Close()},nil
			},
			Run:func(ctx context.Context,p *browser.Page,shot func(string)error)error{return run(ctx,p,f,shot)},
		}
	}
	add("auth.login.required","signed-out","/",func(_ context.Context,p *browser.Page,_ *fixture,shot func(string)error)error{
		if err:=state(p,"login");err!=nil{return err}
		if err:=visible(p,"#login a");err!=nil{return err}
		if err:=checks(p,"() => document.querySelector('#account-menu').hidden");err!=nil{return err}
		return capture(p,shot,"login")
	})
	add("chat.composer.ready","empty-chat","/",func(_ context.Context,p *browser.Page,f *fixture,shot func(string)error)error{
		if err:=state(p,"start");err!=nil{return err}
		if err:=idle(p);err!=nil{return err}
		fill(p,prompt)
		if err:=checks(p,"() => !document.querySelector('#quick').disabled && !document.querySelector('#thoughtful').disabled",
			"() => document.querySelector('#context-info').hidden",
			"() => getComputedStyle(document.querySelector('.headline')).backgroundImage.includes('headline-overlay.svg')");err!=nil{return err}
		if err:=capture(p,shot,"ready");err!=nil{return err}
		// Real keyboard interaction: Enter must insert a newline, never submit.
		fill(p,"Erste Zeile")
		p.MustElement("#content").MustType(input.Enter)
		p.MustElement("#content").MustInput("Zweite Zeile")
		if err:=checks(p,"() => document.querySelector('#content').value === 'Erste Zeile\\nZweite Zeile'");err!=nil{return err}
		f.mu.Lock();n:=len(f.submissions);f.mu.Unlock()
		if n!=0{return fmt.Errorf("Enter submitted a prompt")}
		if err:=testSSE(p);err!=nil{return err}
		return nil
	})
	add("chat.reply.streaming","streaming-reply","/",func(ctx context.Context,p *browser.Page,f *fixture,shot func(string)error)error{
		if err:=state(p,"start");err!=nil{return err}
		if err:=idle(p);err!=nil{return err}
		fill(p,prompt);click(p,"#thoughtful")
		if err:=state(p,"conversation");err!=nil{return err}
		if err:=visible(p,".response-loader");err!=nil{return err}
		if err:=checks(p,"() => document.querySelector('.response-status').textContent === 'Warte auf das Modell …'",
			"() => !document.querySelector('#stop').hidden && document.querySelector('#prompt-input').hidden");err!=nil{return err}
		if err:=capture(p,shot,"waiting");err!=nil{return err}
		if err:=streamCommand(ctx,f,"progress");err!=nil{return err}
		if err:=checks(p,"() => document.querySelector('.response-status').textContent === 'Denkt nach … · 123 Tokens erzeugt'");err!=nil{return err}
		if err:=streamCommand(ctx,f,"partial");err!=nil{return err}
		if err:=checks(p,"() => document.querySelector('.assistant p')?.textContent === 'Marmelade'",
			"() => !document.querySelector('.response-loader')");err!=nil{return err}
		// Busy navigation is blocked.
		p.MustEval("() => document.querySelector('#history-link').click()")
		if err:=state(p,"conversation");err!=nil{return err}
		if err:=streamCommand(ctx,f,"done");err!=nil{return err}
		if err:=idle(p);err!=nil{return err}
		if err:=complete(p);err!=nil{return err}
		// Follow-up uses explicit quick mode; user text is never HTML.
		fill(p,"<img src=x onerror=alert(1)>");click(p,"#quick")
		if err:=checks(p,"() => document.querySelectorAll('.message').length === 4");err!=nil{return err}
		if err:=streamCommand(ctx,f,"done");err!=nil{return err}
		if err:=idle(p);err!=nil{return err}
		if err:=checks(p,"() => !document.querySelector('.user img')",
			"() => document.querySelector('#context-info').textContent === 'Kontext: 25% · 4.096 / 16.384 Tokens'");err!=nil{return err}
		f.mu.Lock();created:=f.creates;sent:=len(f.submissions);mode:=f.submissions[sent-1].Thinking;f.mu.Unlock()
		if created!=1||sent!=2||mode!="quick"{return fmt.Errorf("follow-up did not reuse conversation and mode")}
		fill(p,"Noch eine Frage");click(p,"#thoughtful")
		if err:=checks(p,"() => document.querySelectorAll('.assistant').length === 3");err!=nil{return err}
		if err:=streamCommand(ctx,f,"partial");err!=nil{return err}
		if err:=checks(p,"() => document.querySelector('.assistant:last-child p')?.textContent === 'Marmelade'");err!=nil{return err}
		click(p,"#stop")
		if err:=idle(p);err!=nil{return err}
		if err:=checks(p,"() => document.querySelector('.assistant:last-child small')?.textContent === 'Abgebrochen'");err!=nil{return err}
		f.mu.Lock();sent=len(f.submissions);f.mu.Unlock()
		if sent!=3{return fmt.Errorf("stop resent a prompt")}
		return nil
	})
	add("chat.reply.complete","complete-reply","/#gespraech/00000000000000000000000000000001",func(_ context.Context,p *browser.Page,f *fixture,shot func(string)error)error{
		if err:=complete(p);err!=nil{return err}
		if err:=capture(p,shot,"answer");err!=nil{return err}
		f.mu.Lock();f.omitContext=true;f.mu.Unlock()
		p.MustReload();if err:=idle(p);err!=nil{return err}
		if err:=checks(p,"() => document.querySelector('#context-info').textContent === 'Kontext: unbekannt'");err!=nil{return err}
		f.mu.Lock();f.omitContext=false;f.quick=16384;f.mu.Unlock()
		p.MustReload();if err:=idle(p);err!=nil{return err}
		if err:=checks(p,"() => document.querySelector('#quick').disabled && !document.querySelector('#thoughtful').disabled");err!=nil{return err}
		f.mu.Lock();f.thoughtful=16384;f.mu.Unlock()
		p.MustReload();if err:=visible(p,"#context-full");err!=nil{return err}
		if err:=checks(p,"() => document.querySelector('#quick').disabled && document.querySelector('#thoughtful').disabled && document.querySelector('#prompt-input').hidden");err!=nil{return err}
		click(p,"#context-full a");return state(p,"start")
	})
	add("chat.history.populated","complete-reply","/#historie",func(_ context.Context,p *browser.Page,f *fixture,shot func(string)error)error{
		if err:=state(p,"history");err!=nil{return err}
		if err:=visible(p,"#chats a");err!=nil{return err}
		if err:=checks(p,"() => document.querySelectorAll('#chats li').length === 1",
			"() => document.querySelector('#chats .conversation-title').textContent === 'Was ist Marmelade?'",
			"() => document.querySelector('#chats time').textContent === '16. Oktober 2026'");err!=nil{return err}
		if err:=capture(p,shot,"list");err!=nil{return err}
		click(p,"#chats a");if err:=complete(p);err!=nil{return err}
		p.MustReload();if err:=complete(p);err!=nil{return err}
		click(p,"#history-link");if err:=visible(p,"#chats button");err!=nil{return err}
		click(p,"#chats button");if err:=visible(p,"#delete-dialog[open]");err!=nil{return err}
		f.mu.Lock();f.rejectDelete=true;f.mu.Unlock()
		click(p,"#confirm-delete");if err:=visible(p,"#error");err!=nil{return err}
		if err:=checks(p,"() => document.querySelectorAll('#chats li').length === 1");err!=nil{return err}
		click(p,"#dismiss-error")
		click(p,"#chats button");click(p,"#confirm-delete")
		if err:=checks(p,"() => document.querySelector('#history-status').textContent.startsWith('Hier findest')");err!=nil{return err}
		f.mu.Lock();f.expireNext=true;f.mu.Unlock()
		click(p,"#history-link");if err:=state(p,"login");err!=nil{return err}
		return checks(p,"() => document.querySelector('#account-menu').hidden && !document.querySelector('.message')")
	})
	add("chat.history.empty","empty-chat","/#historie",func(ctx context.Context,p *browser.Page,f *fixture,shot func(string)error)error{
		if err:=state(p,"history");err!=nil{return err}
		if err:=checks(p,"() => document.querySelector('#history-status').textContent.startsWith('Hier findest') && !document.querySelector('#chats li')");err!=nil{return err}
		if err:=capture(p,shot,"list");err!=nil{return err}
		click(p,"#history .text-link");if err:=state(p,"start");err!=nil{return err}
		if err:=idle(p);err!=nil{return err}
		f.mu.Lock();f.rejectNext=true;f.mu.Unlock()
		fill(p,"Frage bleibt erhalten");click(p,"#quick");if err:=visible(p,"#draft-hint");err!=nil{return err}
		if err:=checks(p,"() => document.querySelector('#content').value === 'Frage bleibt erhalten'");err!=nil{return err}
		click(p,"#quick");if err:=visible(p,".response-loader");err!=nil{return err}
		if err:=streamCommand(ctx,f,"done");err!=nil{return err}
		if err:=idle(p);err!=nil{return err}
		f.mu.Lock();created:=f.creates;sent:=len(f.submissions);f.mu.Unlock()
		if created!=1||sent!=1{return fmt.Errorf("retry duplicated conversation or prompt")}
		click(p,"#logout");return state(p,"login")
	})
	add("chat.delete.confirmation","complete-reply","/#gespraech/00000000000000000000000000000001",func(_ context.Context,p *browser.Page,f *fixture,shot func(string)error)error{
		if err:=complete(p);err!=nil{return err}
		click(p,"#delete");if err:=visible(p,"#delete-dialog[open]");err!=nil{return err}
		if err:=checks(p,"() => document.querySelector('#delete-dialog p').textContent.includes('Was ist Marmelade?')");err!=nil{return err}
		if err:=capture(p,shot,"dialog");err!=nil{return err}
		click(p,"#delete-dialog button[value=cancel]")
		if err:=checks(p,"() => !document.querySelector('#delete-dialog').open");err!=nil{return err}
		f.mu.Lock();n:=len(f.chats);f.mu.Unlock();if n!=1{return fmt.Errorf("cancel deleted conversation")}
		click(p,"#delete");click(p,"#confirm-delete")
		if err:=state(p,"history");err!=nil{return err}
		return checks(p,"() => document.querySelector('#history-status').textContent.startsWith('Hier findest')")
	})
	return result
}
