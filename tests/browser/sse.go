package main

import (
	"fmt"

	"github.com/go-rod/rod"
)

// Exercise the production browser ES module, including the former Node unit
// test's byte boundaries and large snapshot. No JS runtime outside Chromium.
func testSSE(p *rod.Page) error {
	result,err:=p.Eval(`async () => {
		const {readEvents}=await import('/sse.js');
		const bytes=new TextEncoder().encode(': comment\r\nevent: delta\r\ndata: {"text":"Gründlich 😀"}\r\n\r\nevent: done\ndata: {"message_id":\ndata: "123"}\n\n');
		const expected=JSON.stringify([['delta',{text:'Gründlich 😀'}],['done',{message_id:'123'}]]);
		for(let size=1;size<=bytes.length;size++){
			const events=[];
			await readEvents(new ReadableStream({start(c){for(let i=0;i<bytes.length;i+=size)c.enqueue(bytes.slice(i,i+size));c.close();}}),(...args)=>events.push(args));
			if(JSON.stringify(events)!==expected)throw Error('SSE chunk size '+size);
		}
		let rejected=false;
		try{await readEvents(new ReadableStream({start(c){c.enqueue(new TextEncoder().encode('data: {}'));c.close();}}),()=>{});}
		catch(e){rejected=e.message.includes('unvollständig');}
		if(!rejected)throw Error('Incomplete SSE frame accepted');
		const html='<p>'+'&lt;'.repeat(300000)+'</p>';
		const snapshot=new TextEncoder().encode('event: snapshot\ndata: '+JSON.stringify({html})+'\n\nevent: done\ndata: {}\n\n');
		const received=[];
		await readEvents(new ReadableStream({start(c){c.enqueue(snapshot);c.close();}}),(...args)=>received.push(args));
		return JSON.stringify(received)===JSON.stringify([['snapshot',{html}],['done',{}]]);
	}`)
	if err!=nil{return err}
	if !result.Value.Bool(){return fmt.Errorf("large SSE snapshot mismatch")}
	return nil
}
