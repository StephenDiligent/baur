package exec

import (
	"fmt"
	"runtime"
	"testing"
)

func TestEchoStdout(t *testing.T) {
	const echoStr = "hello world!"

	// Windows returns StrOutput with surrounding quotation marks
	var expected string
	if runtime.GOOS == "windows" {
		expected = fmt.Sprintf("\"%s\"", echoStr)
	} else {
		expected = echoStr
	}

	var res *Result
	var err error
	if runtime.GOOS == "windows" {
		res, err = Command("cmd", "/C", "echo", echoStr).Run()
	} else {
		res, err = Command("echo", "-n", echoStr).Run()
	}

	if err != nil {
		t.Fatal(err)
	}

	if res.ExitCode != 0 {
		t.Fatalf("cmd exited with code %d, expected 0", res.ExitCode)
	}

	if res.StrOutput() != expected {
		t.Errorf("expected output '%s', got '%s'", expected, res.StrOutput())
	}
}

func TestCommandFails(t *testing.T) {
	var res *Result
	var err error
	if runtime.GOOS == "windows" {
		res, err = Command("cmd", "/C", "exit", "1").Run()
	} else {
		res, err = Command("false").Run()
	}
	if err != nil {
		t.Fatal(err)
	}

	if res.ExitCode != 1 {
		t.Fatalf("cmd exited with code %d, expected 1", res.ExitCode)
	}

	if len(res.Output) != 0 {
		t.Fatalf("expected no output from command but got '%s'", res.StrOutput())
	}
}

func TestExpectSuccess(t *testing.T) {
	var res *Result
	var err error
	if runtime.GOOS == "windows" {
		res, err = Command("cmd", "/C", "exit", "1").ExpectSuccess().Run()
	} else {
		res, err = Command("false").ExpectSuccess().Run()
	}
	if err == nil {
		t.Fatal("Command did not return an error")
	}

	if res != nil {
		t.Fatalf("Command returned an error and result was not nil: %+v", res)
	}

}
