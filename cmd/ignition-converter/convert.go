package main

import (
	"encoding/json"
	"fmt"
	"strings"

	// Unlike encoding/json, omits the empty structs of the Ignition 3 types
	// (e.g. an empty `ignition.config.replace`), as the Ignition tooling does.
	clarketm "github.com/clarketm/json"
	"github.com/coreos/ign-converter/translate/v23tov30"
	"github.com/coreos/ignition/config/v2_3"
	"github.com/coreos/ignition/config/v2_3/types"
)

// rootOnly rejects entries on a filesystem other than the implicit "root".
// Cluster API's Container Linux Config template only writes there, and the
// translator would silently map any undeclared filesystem name to "/".
func rootOnly(s types.Storage) error {
	var nodes []types.Node
	for _, f := range s.Files {
		nodes = append(nodes, f.Node)
	}
	for _, d := range s.Directories {
		nodes = append(nodes, d.Node)
	}
	for _, l := range s.Links {
		nodes = append(nodes, l.Node)
	}
	for _, n := range nodes {
		if n.Filesystem != "root" {
			return fmt.Errorf("%s: filesystem %q is not supported, only \"root\"", n.Path, n.Filesystem)
		}
	}
	return nil
}

// convert turns a spec 2.x Ignition config into spec 3.0 JSON. It reports
// converted=false when value is already spec 3.x, so callers can skip the
// mutation and the webhook stays idempotent.
//
// Caveat: the translator turns a 2.x file with `append: true` into a 3.0 file
// with `overwrite: false` and `append` contents, which matches 2.x semantics.
// Cluster API's own template never sets it; it can only come from a user's
// additional Container Linux Config.
func convert(value []byte) (out []byte, converted bool, err error) {
	var head struct {
		Ignition struct {
			Version string `json:"version"`
		} `json:"ignition"`
	}
	if err := json.Unmarshal(value, &head); err != nil {
		return nil, false, fmt.Errorf("parsing Ignition JSON: %w", err)
	}

	switch major, _, _ := strings.Cut(head.Ignition.Version, "."); major {
	case "3":
		return nil, false, nil
	case "2":
	default:
		return nil, false, fmt.Errorf("unsupported Ignition version %q", head.Ignition.Version)
	}

	// Parse also upgrades 2.0-2.2 to 2.3 and validates the config.
	cfg, rpt, err := v2_3.Parse(value)
	if err != nil {
		return nil, false, fmt.Errorf("parsing Ignition 2.x config: %w: %s", err, rpt.String())
	}
	if err := rootOnly(cfg.Storage); err != nil {
		return nil, false, err
	}
	// The translator needs a spec 3.0 path for every declared filesystem (CABPK's
	// diskSetup.filesystems). That path only tells Ignition where to mount it to
	// write files, and rootOnly leaves none there: an empty one (no path in 3.0)
	// lets Ignition create the filesystem without mounting it, while CABPK's
	// systemd mount units (KubeadmConfig mounts), converted as is, mount it at
	// boot. The translator writes to the map, so it cannot be shared.
	fsMap := map[string]string{"root": "/"}
	for _, fs := range cfg.Storage.Filesystems {
		if fs.Name != "root" {
			fsMap[fs.Name] = ""
		}
	}
	cfg3, err := v23tov30.Translate(cfg, fsMap)
	if err != nil {
		return nil, false, fmt.Errorf("converting Ignition config to spec 3.0: %w", err)
	}
	out, err = clarketm.Marshal(cfg3)
	if err != nil {
		return nil, false, fmt.Errorf("marshaling Ignition 3.0 config: %w", err)
	}
	return out, true, nil
}
