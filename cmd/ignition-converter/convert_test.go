package main

import (
	"embed"
	"net/url"
	"strings"
	"testing"

	"github.com/coreos/ignition/config/v2_3"
	"github.com/coreos/ignition/v2/config/v3_0"
	"github.com/coreos/ignition/v2/config/v3_0/types"
)

// Fixtures are the output of Cluster API's own renderer
// (bootstrap/kubeadm/pkg/ignition: NewInitControlPlane and NewNode, with an
// additional Container Linux Config appending to /etc/hosts), captured by a
// throwaway program. Regenerate them when Cluster API changes its template,
// then replace the PEM contents of /etc/kubernetes/pki with the placeholder
// (no key material in git; e2e.sh generates it).
var fixtures = []string{"testdata/kubeadm-init.json", "testdata/kubeadm-join.json"}

//go:embed testdata
var testdata embed.FS

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := testdata.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// parseV3 validates with Ignition's own spec 3.0 validator, standing in for
// the ignition-validate binary.
func parseV3(t *testing.T, raw []byte) types.Config {
	t.Helper()
	cfg, rpt, err := v3_0.Parse(raw)
	if err != nil {
		t.Fatalf("invalid Ignition 3.0 config: %v\n%s", err, rpt.String())
	}
	if len(rpt.Entries) != 0 {
		t.Errorf("Ignition validator reported:\n%s", rpt.String())
	}
	return cfg
}

func unescape(t *testing.T, source *string) string {
	t.Helper()
	if source == nil {
		t.Fatal("nil file source")
	}
	data, ok := strings.CutPrefix(*source, "data:,")
	if !ok {
		t.Fatalf("source %q is not an inline data URL", *source)
	}
	s, err := url.PathUnescape(data)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestConvertCABPKOutput(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			in := readFixture(t, name)
			old, _, err := v2_3.Parse(in)
			if err != nil {
				t.Fatal(err)
			}

			out, converted, err := convert(in)
			if err != nil || !converted {
				t.Fatalf("convert() = converted %v, err %v", converted, err)
			}
			got := parseV3(t, out)

			if got.Ignition.Version != "3.0.0" {
				t.Errorf("version = %q, want 3.0.0", got.Ignition.Version)
			}

			files := map[string]types.File{}
			for _, f := range got.Storage.Files {
				files[f.Path] = f
			}
			if len(files) != len(old.Storage.Files) {
				t.Fatalf("got %d files, want %d", len(files), len(old.Storage.Files))
			}
			for _, o := range old.Storage.Files {
				f, ok := files[o.Path]
				if !ok {
					t.Errorf("file %s lost", o.Path)
					continue
				}
				if f.Mode == nil || o.Mode == nil || *f.Mode != *o.Mode {
					t.Errorf("%s: mode %v, want %v", o.Path, f.Mode, o.Mode)
				}
				// An appended file moves its contents to Append and must not overwrite.
				src := f.Contents.Source
				if o.Append {
					if len(f.Append) != 1 || f.Overwrite == nil || *f.Overwrite {
						t.Errorf("%s: want one append and overwrite=false, got %+v", o.Path, f)
						continue
					}
					src = f.Append[0].Source
				}
				if unescape(t, src) != unescape(t, &o.Contents.Source) {
					t.Errorf("%s: contents changed", o.Path)
				}
			}

			script := files["/etc/kubeadm.sh"]
			if script.Mode == nil || *script.Mode != 0o700 {
				t.Errorf("/etc/kubeadm.sh mode = %v, want 0700", script.Mode)
			}
			if !strings.Contains(unescape(t, script.Contents.Source), "kubeadm ") {
				t.Error("/etc/kubeadm.sh lost its kubeadm command")
			}
			hosts := files["/etc/hosts"]
			if hosts.Overwrite == nil || *hosts.Overwrite || len(hosts.Append) != 1 {
				t.Errorf("/etc/hosts must be appended, not overwritten: %+v", hosts)
			}

			if len(got.Systemd.Units) != len(old.Systemd.Units) {
				t.Fatalf("got %d units, want %d", len(got.Systemd.Units), len(old.Systemd.Units))
			}
			for i, u := range old.Systemd.Units {
				g := got.Systemd.Units[i]
				if g.Name != u.Name || (g.Contents == nil) != (u.Contents == "") ||
					(g.Contents != nil && *g.Contents != u.Contents) {
					t.Errorf("unit %s changed: %+v", u.Name, g)
				}
			}
			if len(got.Passwd.Users) != len(old.Passwd.Users) {
				t.Errorf("got %d users, want %d", len(got.Passwd.Users), len(old.Passwd.Users))
			}

			// Idempotence: the webhook sees its own output on the next UPDATE.
			again, converted, err := convert(out)
			if err != nil || converted || again != nil {
				t.Errorf("second convert() = %q, converted %v, err %v; want no-op", again, converted, err)
			}
		})
	}
}

// TestConvertDiskSetup uses the filesystem and mount unit of Cluster API's own
// diskSetup and mounts test case (bootstrap/kubeadm/pkg/ignition/clc,
// clc_test.go), without its partition (see TestConvertErrors). The filesystem
// keeps no path: CABPK's mount unit mounts it.
func TestConvertDiskSetup(t *testing.T) {
	in := `{"ignition":{"version":"2.3.0"},
		"storage":{
			"filesystems":[{"name":"test_disk","mount":{"device":"/dev/disk/azure/scsi1/lun0","format":"ext4",
				"label":"test_disk","options":["-F","-E","lazy_itable_init=1,lazy_journal_init=1"],"wipeFilesystem":true}}]},
		"systemd":{"units":[{"name":"var-lib-testdir.mount","enabled":true,
			"contents":"[Unit]\nDescription = Mount test_disk\n\n[Mount]\nWhat=/dev/disk/azure/scsi1/lun0\nWhere=/var/lib/testdir\nOptions=foo\n\n[Install]\nWantedBy=multi-user.target\n"}]}}`
	out, converted, err := convert([]byte(in))
	if err != nil || !converted {
		t.Fatalf("convert() = converted %v, err %v", converted, err)
	}
	got := parseV3(t, out)

	if len(got.Storage.Filesystems) != 1 {
		t.Fatalf("got %d filesystems, want 1", len(got.Storage.Filesystems))
	}
	fs := got.Storage.Filesystems[0]
	if fs.Device != "/dev/disk/azure/scsi1/lun0" || fs.Format == nil || *fs.Format != "ext4" ||
		fs.Label == nil || *fs.Label != "test_disk" || fs.WipeFilesystem == nil || !*fs.WipeFilesystem ||
		len(fs.Options) != 3 || fs.Path != nil {
		t.Errorf("filesystem = %+v", fs)
	}
	if len(got.Systemd.Units) != 1 || got.Systemd.Units[0].Name != "var-lib-testdir.mount" {
		t.Errorf("units = %+v", got.Systemd.Units)
	}
}

func TestConvertErrors(t *testing.T) {
	for name, in := range map[string]string{
		"empty":              ``,
		"garbage":            `not json`,
		"cloud-config":       `#cloud-config`,
		"no version":         `{}`,
		"future major":       `{"ignition":{"version":"4.0.0"}}`,
		"unsupported 2.x":    `{"ignition":{"version":"2.3.0"},"networkd":{"units":[{"name":"a.network","contents":"[Match]"}]}}`,
		"unknown filesystem": `{"ignition":{"version":"2.3.0"},"storage":{"files":[{"filesystem":"data","path":"/a"}]}}`,
		// diskSetup filesystems convert, but nothing may be written to them.
		"file on a diskSetup filesystem": `{"ignition":{"version":"2.3.0"},"storage":{
			"filesystems":[{"name":"data","mount":{"device":"/dev/vdb","format":"xfs"}}],
			"files":[{"filesystem":"data","path":"/a"}]}}`,
		// CABPK renders a diskSetup partition with layout as an empty one, which
		// spec 3 rejects: it needs a number or a label.
		"diskSetup partition": `{"ignition":{"version":"2.3.0"},"storage":{
			"disks":[{"device":"/dev/vdb","partitions":[{}],"wipeTable":true}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if out, converted, err := convert([]byte(in)); err == nil {
				t.Errorf("convert() = %q, converted %v; want error", out, converted)
			}
		})
	}
}
