package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/archaic-work/visual-test/visualtest"
	"github.com/archaic-work/visual-test/visualtest/rod"
)

func revision(root string)(string,error){
	b,err:=exec.Command("git","-C",root,"rev-parse","HEAD").Output()
	return strings.TrimSpace(string(b)),err
}
func run()error{
	root:=flag.String("root","../..","candidate checkout")
	base:=flag.String("base","","stable main checkout")
	out:=flag.String("out","","fresh report directory")
	flag.Parse()
	if *out==""{return fmt.Errorf("-out is required")}
	candidate,err:=filepath.Abs(*root);if err!=nil{return err}
	output,err:=filepath.Abs(*out);if err!=nil{return err}
	if err:=os.MkdirAll(output,0755);err!=nil{return err}
	entries,err:=os.ReadDir(output);if err!=nil{return err};if len(entries)!=0{return fmt.Errorf("output must be empty")}
	rev,err:=revision(candidate);if err!=nil{return err}
	runner:=rod.Runner{Browser:os.Getenv("VISUALTEST_BROWSER_EXECUTABLE"),Locale:"de-DE",Timezone:"Europe/Berlin",NoSandbox:os.Getenv("CI")=="true",Timeout:90*time.Second,
		Viewports:map[string]visualtest.Size{"desktop":{Width:1071,Height:1579},"mobile":{Width:390,Height:844}},Cases:cases()}
	defs,err:=visualtest.Load(filepath.Join(candidate,"tests/scenarios"));if err!=nil{return err}
	if len(defs)!=len(runner.Cases){return fmt.Errorf("scenario catalog and browser cases differ")}
	ctx,cancel:=context.WithTimeout(context.Background(),12*time.Minute);defer cancel()
	current,err:=runner.Capture(ctx,rod.Options{ProjectRoot:candidate,Revision:rev,Output:filepath.Join(output,"candidate")})
	if err!=nil{return err}
	fmt.Printf("Captured %d candidate checkpoints at %s\n",len(current.Images),rev)
	if *base==""{return nil}
	stable,err:=filepath.Abs(*base);if err!=nil{return err}
	stableRev,err:=revision(stable);if err!=nil{return err}
	_,err=runner.Capture(ctx,rod.Options{ProjectRoot:stable,Revision:stableRev,Output:filepath.Join(output,"main"),MatchContracts:current.Contracts})
	if err!=nil{return err}
	summary,err:=visualtest.Compare(filepath.Join(output,"main"),filepath.Join(output,"candidate"),filepath.Join(output,"report"))
	if err!=nil{return err}
	fmt.Print(summary.MarkdownSummary())
	if file:=os.Getenv("GITHUB_STEP_SUMMARY");file!=""{
		f,err:=os.OpenFile(file,os.O_WRONLY|os.O_APPEND,0644);if err!=nil{return err}
		_,err=f.WriteString(summary.MarkdownSummary());closeErr:=f.Close();if err!=nil{return err};return closeErr
	}
	return nil
}
func main(){if err:=run();err!=nil{log.Fatal(err)}}
