package v1alpha1_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
)

// pinnedContainerlabVersion is the containerlab release the vocabulary below was taken from. It
// must track ARG CONTAINERLAB_VERSION in build/launcher.Dockerfile.
const pinnedContainerlabVersion = "0.78.0"

// pinnedContainerlabVocabulary is the yaml vocabulary of the pinned containerlab's node
// definition and its sub objects, keyed by the type name clabernetes uses for the same object.
//
// To refresh it when bumping containerlab, re-read the yaml struct tags of the corresponding
// types in the new release and update both this map and pinnedContainerlabVersion:
//
//	types/node_definition.go -> NodeDefinition
//	types/types.go           -> ConfigDispatcher, Extras, DNSConfig, CertificateConfig, MgmtNet
//	types/component.go       -> Component, XIOM, MDA
//
// Entries clabernetes deliberately does not expose (i.e. stages, credentials, restart-policy)
// are kept, since this map describes containerlab's vocabulary rather than ours -- the test only
// asserts that ours is a subset of it.
var pinnedContainerlabVocabulary = map[string][]string{
	"CertificateConfig": {
		"issue",
		"key-size",
		"sans",
		"validity-duration",
	},
	"Component": {
		"env",
		"mda",
		"sfm",
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
	"Extras": {
		"ceos-copy-to-flash",
		"k8s_kind",
		"mysocket-proxy",
		"srl-agents",
	},
	"MDA": {
		"slot",
		"type",
	},
	"MgmtNet": {
		"bridge",
		"driver-opts",
		"external-access",
		"ipv4-gw",
		"ipv4-range",
		"ipv4-subnet",
		"ipv6-gw",
		"ipv6-range",
		"ipv6-subnet",
		"mtu",
		"network",
		"skip-when-unused",
	},
	"NodeDefinition": {
		"aliases",
		"auto-remove",
		"binds",
		"cap-add",
		"certificate",
		"cgroupns-mode",
		"cmd",
		"components",
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
		"extras",
		"group",
		"healthcheck",
		"image",
		"image-pull-policy",
		"kind",
		"labels",
		"license",
		"link-apply-mode",
		"memory",
		"mgmt-ipv4",
		"mgmt-ipv6",
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

	for idx := range walk.NumField() {
		field := walk.Field(idx)

		tag := strings.Split(field.Tag.Get("yaml"), ",")[0]
		if tag != "" && tag != "-" {
			tags = append(tags, tag)
		}

		collectYAMLTags(field.Type, into)
	}

	into[walk.Name()] = tags
}

// TestNodeVocabularyIsContainerlabSubset is the guard that would have caught the publish,
// sandbox, kernel, wait-for and top-level SANs fields: every yaml tag clabernetes renders into a
// topo.clab.yaml must exist on the matching containerlab object, otherwise the launcher's
// containerlab -- which parses strictly -- rejects the whole topology.
func TestNodeVocabularyIsContainerlabSubset(t *testing.T) {
	ours := map[string][]string{}

	// both roots are rendered into the launcher's topo.clab.yaml
	collectYAMLTags(reflect.TypeFor[clabernetesapisv1alpha1.NodeDefinition](), ours)
	collectYAMLTags(reflect.TypeFor[clabernetesapisv1alpha1.MgmtNet](), ours)

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
			if !slices.Contains(theirs, tag) {
				t.Errorf(
					"%s field %q does not exist in containerlab %s -- the launcher would fail to"+
						" parse a topology using it",
					typeName,
					tag,
					pinnedContainerlabVersion,
				)
			}
		}
	}
}
