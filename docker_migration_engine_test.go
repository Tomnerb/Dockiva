package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSplitArm64Refs(t *testing.T) {
	arch := map[string]string{"alpine:latest": "arm64", "amd:1": "amd64", "nginx:alpine": "arm64\n"}
	keep, skipped := splitArm64Refs([]string{"alpine:latest", "amd:1", "nginx:alpine", "gone:1"}, func(ref string) (string, error) {
		if a, ok := arch[ref]; ok {
			return a, nil
		}
		return "", errors.New("no such image")
	})
	if !reflect.DeepEqual(keep, []string{"alpine:latest", "nginx:alpine"}) || !reflect.DeepEqual(skipped, []string{"amd:1", "gone:1"}) {
		t.Fatalf("keep %v skipped %v", keep, skipped)
	}
}

func TestPickVolumeHelperImage(t *testing.T) {
	for _, c := range []struct {
		refs []string
		want string
	}{
		{[]string{"nginx:alpine", "docker.io/library/alpine:3.20", "busybox:latest"}, "busybox:latest"},
		{[]string{"evil/busybox:latest", "alpine:<none>", "docker.io/library/alpine:3.20"}, "docker.io/library/alpine:3.20"},
		{[]string{"localhost:5000/busybox", "node:22"}, ""},
	} {
		if got := pickVolumeHelperImage(c.refs); got != c.want {
			t.Errorf("%v: got %q, want %q", c.refs, got, c.want)
		}
	}
}

func TestEngineCreateFromInspect(t *testing.T) {
	raw := []byte(`{
		"Id": "3c5ea9a1920b6d515232e4ed79ded00694f29c2f5fff614adf066d8c35436e57",
		"Name": "/shop-db-1",
		"Config": {"Image": "postgres:16-alpine", "Env": ["A=1"], "Cmd": ["postgres"], "Entrypoint": ["docker-entrypoint.sh"],
			"WorkingDir": "/w", "User": "70", "ExposedPorts": {"5432/tcp": {}},
			"Labels": {"com.docker.compose.project": "shop", "com.docker.compose.service": "db"},
			"Healthcheck": {"Test": ["CMD", "pg_isready"], "Interval": 1000000000}},
		"HostConfig": {"NetworkMode": "shop_default", "PortBindings": {"5432/tcp": [{"HostIp": "0.0.0.0", "HostPort": "5433"}]},
			"RestartPolicy": {"Name": "unless-stopped", "MaximumRetryCount": 0}},
		"Mounts": [
			{"Type": "volume", "Name": "shop_pg", "Source": "/var/lib/docker/volumes/shop_pg/_data", "Destination": "/var/lib/postgresql/data", "RW": true},
			{"Type": "bind", "Source": "/Users/me/shop/init", "Destination": "/docker-entrypoint-initdb.d", "RW": false}],
		"NetworkSettings": {"Networks": {"shop_default": {"Aliases": ["shop-db-1", "db", "3c5ea9a1920b"]}, "bridge": {"Aliases": null}}}
	}`)
	name, body, networks, err := engineCreateFromInspect(raw)
	if err != nil {
		t.Fatal(err)
	}
	if name != "shop-db-1" || !reflect.DeepEqual(networks, []string{"shop_default"}) {
		t.Fatalf("name %q networks %v", name, networks)
	}
	data, _ := json.Marshal(body)
	var got map[string]any
	_ = json.Unmarshal(data, &got)
	want := map[string]any{
		"Image": "postgres:16-alpine", "Env": []any{"A=1"}, "Cmd": []any{"postgres"}, "Entrypoint": []any{"docker-entrypoint.sh"},
		"WorkingDir": "/w", "User": "70", "ExposedPorts": map[string]any{"5432/tcp": map[string]any{}},
		"Labels":      map[string]any{"com.docker.compose.project": "shop", "com.docker.compose.service": "db"},
		"Healthcheck": map[string]any{"Test": []any{"CMD", "pg_isready"}, "Interval": float64(1000000000)},
		"HostConfig": map[string]any{
			"NetworkMode":  "shop_default",
			"PortBindings": map[string]any{"5432/tcp": []any{map[string]any{"HostIp": "127.0.0.1", "HostPort": "5433"}}},
			"Mounts": []any{
				map[string]any{"Type": "volume", "Source": "shop_pg", "Target": "/var/lib/postgresql/data"},
				map[string]any{"Type": "bind", "Source": "/Users/me/shop/init", "Target": "/docker-entrypoint-initdb.d", "ReadOnly": true},
			},
			"RestartPolicy": map[string]any{"Name": "unless-stopped"},
		},
		"NetworkingConfig": map[string]any{"EndpointsConfig": map[string]any{"shop_default": map[string]any{"Aliases": []any{"shop-db-1", "db"}}}},
	}
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", " ")
		t.Fatalf("create body:\n%s", gotJSON)
	}

	// The default bridge stays the engine default, and a null healthcheck is dropped.
	// An explicit empty Cmd stays empty, while a missing one keeps the image default.
	_, body, networks, err = engineCreateFromInspect([]byte(`{"Name":"/x","Config":{"Image":"alpine","Entrypoint":["/bin/true"],"Cmd":[],"Healthcheck":null},"HostConfig":{"NetworkMode":"bridge"}}`))
	if err != nil || body.HostConfig.NetworkMode != "" || body.Healthcheck != nil || len(networks) != 0 {
		t.Fatalf("bridge: %+v %v %v", body, networks, err)
	}
	if data, _ := json.Marshal(body); !strings.Contains(string(data), `"Cmd":[],"Entrypoint":["/bin/true"]`) {
		t.Fatalf("explicit empty Cmd lost: %s", data)
	}
}

type tarEntry struct {
	hdr  tar.Header
	body string
}

func buildTar(t *testing.T, entries ...tarEntry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		e.hdr.Size = int64(len(e.body))
		if e.hdr.Mode == 0 {
			e.hdr.Mode = 0o644
		}
		if err := tw.WriteHeader(&e.hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestExtractTarSafely(t *testing.T) {
	dir := t.TempDir()
	sparse := "head" + strings.Repeat("\x00", 300*1024) + "tail"
	archive := buildTar(t,
		tarEntry{hdr: tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0o750}},
		tarEntry{hdr: tar.Header{Name: "./data/", Typeflag: tar.TypeDir, Mode: 0o555}},
		tarEntry{hdr: tar.Header{Name: "./data/a.txt", Typeflag: tar.TypeReg, Mode: 0o640}, body: "hello"},
		tarEntry{hdr: tar.Header{Name: "./implicit/parent/b", Typeflag: tar.TypeReg}, body: "b"},
		tarEntry{hdr: tar.Header{Name: "./abs-link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}},
		tarEntry{hdr: tar.Header{Name: "./hard", Typeflag: tar.TypeLink, Linkname: "./data/a.txt"}},
		tarEntry{hdr: tar.Header{Name: "./fifo", Typeflag: tar.TypeFifo}},
		tarEntry{hdr: tar.Header{Name: "./sparse", Typeflag: tar.TypeReg}, body: sparse},
		tarEntry{hdr: tar.Header{Name: "./zeros", Typeflag: tar.TypeReg}, body: strings.Repeat("\x00", 70000)},
	)
	if err := extractTarSafely(archive, dir); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "data", "a.txt")); string(data) != "hello" {
		t.Fatalf("a.txt = %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "hard")); string(data) != "hello" {
		t.Fatalf("hard link = %q", data)
	}
	if link, err := os.Readlink(filepath.Join(dir, "abs-link")); err != nil || link != "/etc/passwd" {
		t.Fatalf("symlink kept as %q, %v", link, err)
	}
	if info, _ := os.Stat(filepath.Join(dir, "data")); info.Mode().Perm() != 0o555 {
		t.Fatalf("data mode %v", info.Mode())
	}
	if info, _ := os.Stat(filepath.Join(dir, "data", "a.txt")); info.Mode().Perm() != 0o640 {
		t.Fatalf("a.txt mode %v", info.Mode())
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "sparse")); string(data) != sparse {
		t.Fatalf("sparse file has %d bytes", len(data))
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "zeros")); len(data) != 70000 || strings.Trim(string(data), "\x00") != "" {
		t.Fatalf("zero file has %d bytes", len(data))
	}
	if _, err := os.Lstat(filepath.Join(dir, "fifo")); err == nil {
		t.Fatal("fifo was created")
	}
	_ = os.Chmod(filepath.Join(dir, "data"), 0o755) // so TempDir cleanup can remove it

	outside := t.TempDir()
	for name, entries := range map[string][]tarEntry{
		"dotdot":   {{hdr: tar.Header{Name: "../escape", Typeflag: tar.TypeReg}, body: "x"}},
		"inner ..": {{hdr: tar.Header{Name: "a/../../escape", Typeflag: tar.TypeReg}, body: "x"}},
		"absolute": {{hdr: tar.Header{Name: "/escape", Typeflag: tar.TypeReg}, body: "x"}},
		"through symlink": {
			{hdr: tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: outside}},
			{hdr: tar.Header{Name: "link/escape", Typeflag: tar.TypeReg}, body: "x"},
		},
		"symlink in-root parent": {
			{hdr: tar.Header{Name: "real/", Typeflag: tar.TypeDir}},
			{hdr: tar.Header{Name: "alias", Typeflag: tar.TypeSymlink, Linkname: "real"}},
			{hdr: tar.Header{Name: "alias/x", Typeflag: tar.TypeReg}, body: "x"},
		},
		"overwrite symlink": {
			{hdr: tar.Header{Name: "f", Typeflag: tar.TypeSymlink, Linkname: filepath.Join(outside, "escape")}},
			{hdr: tar.Header{Name: "f/", Typeflag: tar.TypeDir}},
		},
		"hard link out": {{hdr: tar.Header{Name: "h", Typeflag: tar.TypeLink, Linkname: "../../etc/passwd"}}},
	} {
		target := t.TempDir()
		err := extractTarSafely(buildTar(t, entries...), target)
		if err == nil {
			t.Errorf("%s: extraction succeeded", name)
		}
		if _, statErr := os.Lstat(filepath.Join(outside, "escape")); statErr == nil {
			t.Fatalf("%s: wrote outside the volume", name)
		}
	}

	// A truncated stream is an error, not a short file.
	full := buildTar(t, tarEntry{hdr: tar.Header{Name: "big", Typeflag: tar.TypeReg}, body: strings.Repeat("x", 4096)}).Bytes()
	if err := extractTarSafely(bytes.NewReader(full[:2048]), t.TempDir()); err == nil {
		t.Fatal("truncated archive extracted")
	}

	// A regular file entry replaces a symlink rather than writing through it.
	target := t.TempDir()
	victim := filepath.Join(outside, "victim")
	_ = os.WriteFile(victim, []byte("keep"), 0o644)
	err := extractTarSafely(buildTar(t,
		tarEntry{hdr: tar.Header{Name: "f", Typeflag: tar.TypeSymlink, Linkname: victim}},
		tarEntry{hdr: tar.Header{Name: "f", Typeflag: tar.TypeReg}, body: "new"},
	), target)
	if data, _ := os.ReadFile(victim); err != nil || string(data) != "keep" {
		t.Fatalf("victim = %q, err %v", data, err)
	}
	if info, _ := os.Lstat(filepath.Join(target, "f")); !info.Mode().IsRegular() {
		t.Fatal("symlink was not replaced")
	}
	if got, err := safeTarPath("./a/./b"); err != nil || got != "a/b" {
		t.Fatalf("safeTarPath = %q, %v", got, err)
	}
}
