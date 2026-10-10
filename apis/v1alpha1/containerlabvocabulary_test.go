package v1alpha1_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
)

// pinnedContainerlabVersion is the containerlab release the vocabulary below was taken from. It
// must track the github.com/srl-labs/containerlab module version pinned in go.mod.
const pinnedContainerlabVersion = "v0.80.1-0.20261010165050-65f5eac62991"

// pinnedContainerlabVocabulary is the yaml vocabulary of the pinned containerlab's node
// definition and its sub objects, keyed by the type name clabernetes uses for the same object.
//
// To refresh it when bumping containerlab, re-read the yaml struct tags of the corresponding
// types in the new release and update both this map and pinnedContainerlabVersion:
//
//	types/node_definition.go -> NodeDefinition
//	types/types.go           -> ConfigDispatcher, DNSConfig, CertificateConfig,
//	                            HealthcheckConfig
//	nodes/sros/component.go  -> Component, XIOM, MDA (nokia_srsim)
//	nodes/vr_sros/vr-sros.go -> Component, XIOM, MDA (nokia_sros)
//
// Entries clabernetes deliberately does not expose (i.e. stages, credentials, runtime)
// are kept, since this map describes containerlab's vocabulary rather than ours -- the test only
// asserts that ours is a subset of it. The Component snapshot is the union of the per-kind
// component shapes: nokia_srsim carries env, and nokia_sros carries the typed cpu, ram, and
// max-nics resources. The node `extras` field and its `srl-agents`, `ceos-copy-to-flash`, and
// `frr` sub objects are gone since the kind-specific config migration: their keys are now
// kind-specific config keys validated by the owning kind (`copy-to-flash` for arista_ceos,
// `daemons` for frr/frrouting).
var pinnedContainerlabVocabulary = map[string][]string{
	"CertificateConfig": {
		"issue",
		"key-size",
		"sans",
		"validity-duration",
	},
	"Component": {
		"cpu",
		"env",
		"max-nics",
		"mda",
		"ram",
		"slot",
		"type",
		"xiom",
	},
	"ConfigDispatcher": {
		"vars",
	},
	"DNSConfig": {
		"options",
		"search",
		"servers",
	},
	"HealthcheckConfig": {
		"test",
		"interval",
		"timeout",
		"retries",
		"start-period",
	},
	"MDA": {
		"slot",
		"type",
	},
	"NodeDefinition": {
		"aliases",
		"auto-remove",
		"binds",
		"cap-add",
		"certificate",
		"cgroup-parent",
		"cgroupns-mode",
		"cmd",
		"config",
		"cpu",
		"cpu-set",
		"credentials",
		"devices",
		"dns",
		"enforce-startup-config",
		"entrypoint",
		"env",
		"env-files",
		"exec",
		"group",
		"healthcheck",
		"hostname",
		"image",
		"image-pull-policy",
		"kind",
		"labels",
		"license",
		"link-apply-mode",
		"memory",
		"mgmt-ipv4",
		"mgmt-ipv6",
		"mgmt-net",
		"network-mode",
		"pid-mode",
		"ports",
		"position",
		"privileged",
		"restart-policy",
		"runtime",
		"security-opts",
		"shm-size",
		"stages",
		"startup-config",
		"startup-delay",
		"suppress-startup-config",
		"sysctls",
		"tmpfs",
		"type",
		"user",
		"volumes",
	},
	"XIOM": {
		"mda",
		"slot",
		"type",
	},
}

// collectYAMLTags walks the given type, recording the yaml tag of every field of every struct
// declared in the api package that is reachable from it. Types from other packages terminate the
// walk: they are not containerlab vocabulary (i.e. the arbitrary JSON behind config.vars).
func collectYAMLTags(walk reflect.Type, into map[string][]string) {
	apiPackage := reflect.TypeFor[clabernetesapisv1alpha1.NodeDefinition]().PkgPath()

	// unwrap to the underlying type -- for maps that is the value type, the only side that can
	// hold vocabulary
	for walk.Kind() == reflect.Pointer || walk.Kind() == reflect.Slice ||
		walk.Kind() == reflect.Array || walk.Kind() == reflect.Map {
		walk = walk.Elem()
	}

	if walk.Kind() != reflect.Struct || walk.PkgPath() != apiPackage {
		return
	}

	if _, alreadyWalked := into[walk.Name()]; alreadyWalked {
		return
	}

	tags := make([]string, 0, walk.NumField())
	into[walk.Name()] = tags

	for field := range walk.Fields() {
		tag, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if tag != "" && tag != "-" {
			tags = append(tags, tag)
		}

		collectYAMLTags(field.Type, into)
	}

	into[walk.Name()] = tags
}

// TestNodeVocabularyIsContainerlabSubset is the guard that would have caught the publish,
// sandbox, kernel, wait-for and top-level SANs fields: every yaml tag clabernetes serializes
// toward the imported containerlab module must exist on the matching containerlab object,
// otherwise the module's strict definition decoding rejects the whole node.
func TestNodeVocabularyIsContainerlabSubset(t *testing.T) {
	ours := map[string][]string{}

	// The Node root is serialized into the planning input definition. File-level management
	// vocabulary is imported directly from containerlab and needs no c9s snapshot.
	collectYAMLTags(reflect.TypeFor[clabernetesapisv1alpha1.NodeDefinition](), ours)

	for typeName, tags := range ours {
		theirs, ok := pinnedContainerlabVocabulary[typeName]
		if !ok {
			t.Errorf(
				"type %q is rendered into containerlab topologies but is not in the containerlab"+
					" %s vocabulary snapshot",
				typeName,
				pinnedContainerlabVersion,
			)

			continue
		}

		for _, tag := range tags {
			// `components` is kind-specific config since containerlab 0.80: it is no longer a
			// field of the generic node definition, but each kind that owns it (the Nokia kinds
			// and SR Linux) validates the value strictly through the kind-specific config
			// decoder when the node is planned, so the vocabulary guard the field serves is the
			// plan-time decode rather than this snapshot.
			if typeName == "NodeDefinition" && tag == "components" {
				continue
			}

			if !slices.Contains(theirs, tag) {
				t.Errorf(
					"%s field %q does not exist in containerlab %s -- the device runtime would fail to"+
						" parse a topology using it",
					typeName,
					tag,
					pinnedContainerlabVersion,
				)
			}
		}
	}
}
