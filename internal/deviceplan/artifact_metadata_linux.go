//go:build linux

package deviceplan

import (
	"errors"
	"os"
	"slices"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	maxGeneratedExtendedAttributes      = 128
	maxGeneratedExtendedAttributeBytes  = 64 << 10
	maxGeneratedExtendedAttributesBytes = 1 << 20
)

func readGeneratedArtifactMetadata(
	path string,
	symlink bool,
	baselineUID,
	baselineGID int,
) (*int64, *int64, []generatedExtendedAttribute, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, nil, nil, &Error{
			Code: ErrorUnsupported, Field: "workspace",
			Behavior: "generated-ownership",
			Message:  "imported artifact ownership is unavailable",
		}
	}
	var uid, gid *int64
	if int(stat.Uid) != baselineUID {
		value := int64(stat.Uid)
		uid = &value
	}
	if int(stat.Gid) != baselineGID {
		value := int64(stat.Gid)
		gid = &value
	}
	attributes, err := readGeneratedExtendedAttributes(path, symlink)
	if err != nil {
		return nil, nil, nil, err
	}

	return uid, gid, attributes, nil
}

func readGeneratedExtendedAttributes(
	path string,
	symlink bool,
) ([]generatedExtendedAttribute, error) {
	list := unix.Listxattr
	get := unix.Getxattr
	if symlink {
		list = unix.Llistxattr
		get = unix.Lgetxattr
	}
	size, err := list(path, nil)
	if errors.Is(err, unix.ENOTSUP) {
		return nil, nil
	}
	if err != nil {
		return nil, generatedMetadataError("cannot list imported artifact extended attributes", err)
	}
	if size == 0 {
		return nil, nil
	}
	if size > maxGeneratedExtendedAttributesBytes {
		return nil, generatedMetadataError(
			"imported artifact extended attributes exceed the limit",
			nil,
		)
	}
	rawNames := make([]byte, size)
	read, err := list(path, rawNames)
	if err != nil {
		return nil, generatedMetadataError("cannot read imported artifact attribute names", err)
	}
	names := strings.Split(strings.TrimSuffix(string(rawNames[:read]), "\x00"), "\x00")
	if len(names) > maxGeneratedExtendedAttributes {
		return nil, generatedMetadataError(
			"imported artifact has too many extended attributes",
			nil,
		)
	}
	slices.Sort(names)
	result := make([]generatedExtendedAttribute, 0, len(names))
	total := 0
	for _, name := range names {
		if name == "" || len(name) > 255 || strings.ContainsRune(name, 0) {
			return nil, generatedMetadataError(
				"imported artifact has an invalid attribute name",
				nil,
			)
		}
		valueSize, getErr := get(path, name, nil)
		if getErr != nil || valueSize > maxGeneratedExtendedAttributeBytes ||
			total+valueSize > maxGeneratedExtendedAttributesBytes {
			return nil, generatedMetadataError(
				"cannot read bounded imported artifact attribute metadata",
				getErr,
			)
		}
		value := make([]byte, valueSize)
		if valueSize != 0 {
			valueSize, getErr = get(path, name, value)
			if getErr != nil {
				return nil, generatedMetadataError(
					"cannot read imported artifact attribute metadata",
					getErr,
				)
			}
			value = value[:valueSize]
		}
		total += len(value)
		result = append(result, generatedExtendedAttribute{
			Name: name, Digest: Digest(value), value: value,
		})
	}

	return result, nil
}

func applyGeneratedArtifactMetadata(
	path string,
	symlink bool,
	uid,
	gid *int64,
	attributes []generatedExtendedAttribute,
) error {
	uidValue, gidValue := -1, -1
	if uid != nil {
		uidValue = int(*uid)
	}
	if gid != nil {
		gidValue = int(*gid)
	}
	if uid != nil || gid != nil {
		var err error
		if symlink {
			err = os.Lchown(path, uidValue, gidValue)
		} else {
			err = os.Chown(path, uidValue, gidValue)
		}
		if err != nil {
			return generatedMetadataError("cannot apply imported artifact ownership", err)
		}
	}
	set := unix.Setxattr
	if symlink {
		set = unix.Lsetxattr
	}
	for _, attribute := range attributes {
		if err := set(path, attribute.Name, attribute.value, 0); err != nil {
			return generatedMetadataError(
				"cannot apply imported artifact extended attribute",
				err,
			)
		}
	}

	return nil
}

func generatedMetadataError(message string, cause error) *Error {
	return &Error{
		Code: ErrorUnsupported, Field: "workspace",
		Behavior: "generated-filesystem-metadata", Message: message, cause: cause,
	}
}
