package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.uber.org/zap"
)

type fakeS3 struct {
	objects map[string][]byte
	puts    map[string][]byte
}

func (f *fakeS3) GetObject(ctx context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	b, ok := f.objects[*in.Key]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(b))}, nil
}

func (f *fakeS3) PutObject(ctx context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if f.puts == nil {
		f.puts = map[string][]byte{}
	}
	b, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	f.puts[*in.Key] = b
	return &s3.PutObjectOutput{}, nil
}

const pluginSrc = `package main
import (
    "fmt"
    "io"
    "strings"
)

func Parse(r io.Reader) ([]map[string]string, error) {
    b, err := io.ReadAll(r)
    if err != nil {
        return nil, err
    }
    lines := strings.Split(strings.TrimSpace(string(b)), "\n")
    if len(lines) == 0 {
        return nil, nil
    }
    hdr := strings.Split(lines[0], "|")
    var rows []map[string]string
    for _, line := range lines[1:] {
        if line == "" {continue}
        cols := strings.Split(line, "|")
        if len(cols) != len(hdr) {
            return nil, fmt.Errorf("malformed")
        }
        m := make(map[string]string)
        for i, h := range hdr {
            m[h] = cols[i]
        }
        rows = append(rows, m)
    }
    return rows, nil
}
`

func buildPlugin(t *testing.T, id string) {
	dir := "/opt/plugins"
	os.MkdirAll(dir, 0755)
	src := filepath.Join(t.TempDir(), "p.go")
	os.WriteFile(src, []byte(pluginSrc), 0644)
	out := filepath.Join(dir, id+".so")
	cmd := exec.Command("go", "build", "-buildmode=plugin", "-o", out, src)
	if outb, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plugin build: %v\n%s", err, outb)
	}
}

func newEvent(key string, size int64) events.S3Event {
	return events.S3Event{Records: []events.S3EventRecord{{S3: events.S3Entity{Bucket: events.S3Bucket{Name: "b"}, Object: events.S3Object{Key: key, Size: size}}}}}
}

func TestHandler(t *testing.T) {
	log = zap.NewNop().Sugar()
	buildPlugin(t, "csv_pipe")

	t.Run("small", func(t *testing.T) {
		os.Setenv("PARSER_ID", "csv_pipe")
		os.Setenv("PROFILE_JSON", `{"required":["header1","header2"]}`)
		f := &fakeS3{objects: map[string][]byte{"f.qns": []byte("header1|header2\n v1 | v2 ")}}
		s3Client = f
		out, err := handler(context.Background(), newEvent("f.qns", 10))
		if err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if len(out.Rows) != 1 || out.BadRows != 0 || len(out.Keys) != 0 {
			t.Fatalf("unexpected output: %+v", out)
		}
	})

	t.Run("large", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString("header1|header2\n")
		for i := 0; i < 2500; i++ {
			sb.WriteString(fmt.Sprintf("a%03d|b%03d\n", i, i))
		}
		f := &fakeS3{objects: map[string][]byte{"big.qns": []byte(sb.String())}}
		s3Client = f
		os.Setenv("PROFILE_JSON", `{"required":["header1","header2"]}`)
		out, err := handler(context.Background(), newEvent("big.qns", 30000000))
		if err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if len(out.Keys) != 3 || out.BadRows != 0 || len(out.Rows) != 0 {
			t.Fatalf("unexpected output: %+v", out)
		}
		if len(f.puts) != 3 {
			t.Fatalf("expected 3 chunks, got %d", len(f.puts))
		}
	})

	t.Run("missing column", func(t *testing.T) {
		f := &fakeS3{objects: map[string][]byte{"bad.qns": []byte("header1\nval")}}
		s3Client = f
		os.Setenv("PROFILE_JSON", `{"required":["header1","header2"]}`)
		if _, err := handler(context.Background(), newEvent("bad.qns", 10)); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("malformed", func(t *testing.T) {
		f := &fakeS3{objects: map[string][]byte{"m.qns": []byte("header1|header2\nval1")}}
		s3Client = f
		os.Setenv("PROFILE_JSON", `{"required":[]}`)
		if _, err := handler(context.Background(), newEvent("m.qns", 10)); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRun(t *testing.T) {
	called := false
	lambdaStart = func(i interface{}) { called = true }
	if err := run(); err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !called {
		t.Fatal("start not called")
	}
}
