//go:build linux

package deviceplan

import (
	"reflect"
	"testing"
)

func TestGeneratedMetadataExcludesHostSELinuxLabels(t *testing.T) {
	t.Parallel()

	for _, portable := range []bool{false, true} {
		read := func(label string) []generatedExtendedAttribute {
			t.Helper()

			names := "security.selinux\x00"
			values := map[string][]byte{"security.selinux": []byte(label)}
			if portable {
				names += "user.package\x00security.capability\x00"
				values["user.package"] = []byte("package metadata")
				values["security.capability"] = []byte("capability metadata")
			}

			attributes, err := readGeneratedExtendedAttributesWith("artifact",
				func(_ string, dest []byte) (int, error) {
					copy(dest, names)

					return len(names), nil
				},
				func(_, name string, dest []byte) (int, error) {
					value := values[name]
					copy(dest, value)

					return len(value), nil
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			return attributes
		}

		planned := read("system_u:object_r:container_file_t:s0\x00")
		prepared := read("system_u:object_r:container_var_lib_t:s0\x00")
		if !reflect.DeepEqual(planned, prepared) {
			t.Fatalf(
				"host labels changed portable metadata: planned=%#v prepared=%#v",
				planned,
				prepared,
			)
		}

		var want []generatedExtendedAttribute
		if portable {
			want = []generatedExtendedAttribute{
				{
					Name:   "security.capability",
					Digest: Digest([]byte("capability metadata")),
					value:  []byte("capability metadata"),
				},
				{
					Name:   "user.package",
					Digest: Digest([]byte("package metadata")),
					value:  []byte("package metadata"),
				},
			}
		}
		if !reflect.DeepEqual(planned, want) {
			t.Fatalf("portable attributes = %#v, want %#v", planned, want)
		}
	}
}
