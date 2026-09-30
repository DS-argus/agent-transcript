package process

import (
	"os"
	"reflect"
	"testing"
)

func TestRunUsesUTCForProcessBirthTimes(t *testing.T) {
	t.Setenv("TZ", "Asia/Seoul")
	t.Setenv("LC_ALL", "C")
	got, err := Run("sh", "-c", `printf '%s|%s' "$TZ" "$LC_ALL"`)
	if err != nil || string(got) != "UTC|C" {
		t.Fatalf("registry time environment: %q %v", got, err)
	}
	if os.Getenv("TZ") != "Asia/Seoul" {
		t.Fatal("parent environment changed")
	}
}
func TestOpenFilesIsBoundedAndDeduplicated(t *testing.T) {
	run := func(name string, args ...string) ([]byte, error) {
		if name != "lsof" || !reflect.DeepEqual(args, []string{"-a", "-p", "100,101", "-Fn"}) {
			t.Fatalf("unbounded inspection: %s %v", name, args)
		}
		return []byte("p100\nn/tmp/b.jsonl\nn/tmp/a.jsonl\nn/tmp/a.jsonl\nn/tmp/not-json.txt\nnrelative.jsonl\n"), nil
	}
	got, err := OpenFiles([]int{100, 101}, run)
	if err != nil || !reflect.DeepEqual(got, []string{"/tmp/a.jsonl", "/tmp/b.jsonl"}) {
		t.Fatalf("owned files: %v %v", got, err)
	}
}
