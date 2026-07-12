#!/usr/bin/env python3
import json
import re
import sys
from pathlib import Path

from ruamel.yaml import YAML

yaml = YAML(typ='safe')

# Preserve the established schema/path order so regenerating on a different
# filesystem does not reorder the OpenAPI document and generated UI client.
CRD_ORDER = {
    name: index
    for index, name in enumerate((
        "clabernetes.containerlab.dev_configs.yaml",
        "clabernetes.containerlab.dev_imagerequests.yaml",
        "clabernetes.containerlab.dev_topologies.yaml",
        "clabernetes.containerlab.dev_links.yaml",
        "clabernetes.containerlab.dev_nodes.yaml",
    ))
}


def main():
    try:
        clabernetes_version = sys.argv[1]
    except IndexError:
        version_pattern = re.compile(r"(?:Version = \")(.*?)\"")

        with open("constants/common.go", "r") as f:
            contents = f.read()

            clabernetes_version_match = re.search(version_pattern, contents)

            if not clabernetes_version_match:
                print(
                    "no version provided, and failed to glean version from source, cannot continue"
                )

                sys.exit(1)

            clabernetes_version = clabernetes_version_match.groups()[0]

    out = {
        "openapi": "3.0.3",
        "info": {
            "description": "clabernetes openapi v3 spec",
            "title": "clabernetes api",
            "version": clabernetes_version,
        },
        "components": {
            "schemas": {
                "io.k8s.apimachinery.pkg.apis.meta.v1.DeleteOptions": {
                    "description": "DeleteOptions may be provided when deleting an API object.",
                    "properties": {
                        "apiVersion": {
                            "description": "APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources",
                            "type": "string"
                        },
                        "dryRun": {
                            "description": "When present, indicates that modifications should not be persisted. An invalid or unrecognized dryRun directive will result in an error response and no further processing of the request. Valid values are: - All: all dry run stages will be processed",
                            "items": {
                                "default": "",
                                "type": "string"
                            },
                            "type": "array"
                        },
                        "gracePeriodSeconds": {
                            "description": "The duration in seconds before the object should be deleted. Value must be non-negative integer. The value zero indicates delete immediately. If this value is nil, the default grace period for the specified type will be used. Defaults to a per object value if not specified. zero means delete immediately.",
                            "format": "int64",
                            "type": "integer"
                        },
                        "kind": {
                            "description": "Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds",
                            "type": "string"
                        },
                        "orphanDependents": {
                            "description": "Deprecated: please use the PropagationPolicy, this field will be deprecated in 1.7. Should the dependent objects be orphaned. If true/false, the \"orphan\" finalizer will be added to/removed from the object's finalizers list. Either this field or PropagationPolicy may be set, but not both.",
                            "type": "boolean"
                        },
                        "preconditions": {
                            "allOf": [
                                {
                                    "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.Preconditions"
                                }
                            ],
                            "description": "Must be fulfilled before a deletion is carried out. If not possible, a 409 Conflict status will be returned."
                        },
                        "propagationPolicy": {
                            "description": "Whether and how garbage collection will be performed. Either this field or OrphanDependents may be set, but not both. The default policy is decided by the existing finalizer set in the metadata.finalizers and the resource-specific default policy. Acceptable values are: 'Orphan' - orphan the dependents; 'Background' - allow the garbage collector to delete the dependents in the background; 'Foreground' - a cascading policy that deletes all dependents in the foreground.",
                            "type": "string"
                        }
                    },
                    "type": "object"
                },
                "io.k8s.apimachinery.pkg.apis.meta.v1.FieldsV1": {
                    "description": "FieldsV1 stores a set of fields in a data structure like a Trie, in JSON format.\n\nEach key is either a '.' representing the field itself, and will always map to an empty set, or a string representing a sub-field or item. The string will follow one of these four formats: 'f:<name>', where <name> is the name of a field in a struct, or key in a map 'v:<value>', where <value> is the exact json formatted value of a list item 'i:<index>', where <index> is position of a item in a list 'k:<keys>', where <keys> is a map of  a list item's key fields to their unique values If a key maps to an empty Fields value, the field that key represents is part of the set.\n\nThe exact format is defined in sigs.k8s.io/structured-merge-diff",
                    "type": "object"
                },
                "io.k8s.apimachinery.pkg.apis.meta.v1.ListMeta": {
                    "description": "ListMeta describes metadata that synthetic resources must have, including lists and various status objects. A resource may have only one of {ObjectMeta, ListMeta}.",
                    "properties": {
                        "continue": {
                            "description": "continue may be set if the user set a limit on the number of items returned, and indicates that the server has more data available. The value is opaque and may be used to issue another request to the endpoint that served this list to retrieve the next set of available objects. Continuing a consistent list may not be possible if the server configuration has changed or more than a few minutes have passed. The resourceVersion field returned when using this continue value will be identical to the value in the first response, unless you have received this token from an error message.",
                            "type": "string"
                        },
                        "remainingItemCount": {
                            "description": "remainingItemCount is the number of subsequent items in the list which are not included in this list response. If the list request contained label or field selectors, then the number of remaining items is unknown and the field will be left unset and omitted during serialization. If the list is complete (either because it is not chunking or because this is the last chunk), then there are no more remaining items and this field will be left unset and omitted during serialization. Servers older than v1.15 do not set this field. The intended use of the remainingItemCount is *estimating* the size of a collection. Clients should not rely on the remainingItemCount to be set or to be exact.",
                            "format": "int64",
                            "type": "integer"
                        },
                        "resourceVersion": {
                            "description": "String that identifies the server's internal version of this object that can be used by clients to determine when objects have changed. Value must be treated as opaque by clients and passed unmodified back to the server. Populated by the system. Read-only. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#concurrency-control-and-consistency",
                            "type": "string"
                        },
                        "selfLink": {
                            "description": "Deprecated: selfLink is a legacy read-only field that is no longer populated by the system.",
                            "type": "string"
                        }
                    },
                    "type": "object"
                },
                "io.k8s.apimachinery.pkg.apis.meta.v1.ManagedFieldsEntry": {
                    "description": "ManagedFieldsEntry is a workflow-id, a FieldSet and the group version of the resource that the fieldset applies to.",
                    "properties": {
                        "apiVersion": {
                            "description": "APIVersion defines the version of this resource that this field set applies to. The format is \"group/version\" just like the top-level APIVersion field. It is necessary to track the version of a field set because it cannot be automatically converted.",
                            "type": "string"
                        },
                        "fieldsType": {
                            "description": "FieldsType is the discriminator for the different fields format and version. There is currently only one possible value: \"FieldsV1\"",
                            "type": "string"
                        },
                        "fieldsV1": {
                            "allOf": [
                                {
                                    "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.FieldsV1"
                                }
                            ],
                            "description": "FieldsV1 holds the first JSON version format as described in the \"FieldsV1\" type."
                        },
                        "manager": {
                            "description": "Manager is an identifier of the workflow managing these fields.",
                            "type": "string"
                        },
                        "operation": {
                            "description": "Operation is the type of operation which lead to this ManagedFieldsEntry being created. The only valid values for this field are 'Apply' and 'Update'.",
                            "type": "string"
                        },
                        "subresource": {
                            "description": "Subresource is the name of the subresource used to update that object, or empty string if the object was updated through the main resource. The value of this field is used to distinguish between managers, even if they share the same name. For example, a status update will be distinct from a regular update using the same manager name. Note that the APIVersion field is not related to the Subresource field and it always corresponds to the version of the main resource.",
                            "type": "string"
                        },
                        "time": {
                            "allOf": [
                                {
                                    "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.Time"
                                }
                            ],
                            "description": "Time is the timestamp of when the ManagedFields entry was added. The timestamp will also be updated if a field is added, the manager changes any of the owned fields value or removes a field. The timestamp does not update when a field is removed from the entry because another manager took it over."
                        }
                    },
                    "type": "object"
                },
                "io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta": {
                    "description": "ObjectMeta is metadata that all persisted resources must have, which includes all objects users must create.",
                    "properties": {
                        "annotations": {
                            "additionalProperties": {
                                "default": "",
                                "type": "string"
                            },
                            "description": "Annotations is an unstructured key value map stored with a resource that may be set by external tools to store and retrieve arbitrary metadata. They are not queryable and should be preserved when modifying objects. More info: http://kubernetes.io/docs/user-guide/annotations",
                            "type": "object"
                        },
                        "creationTimestamp": {
                            "allOf": [
                                {
                                    "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.Time"
                                }
                            ],
                            "default": {},
                            "description": "CreationTimestamp is a timestamp representing the server time when this object was created. It is not guaranteed to be set in happens-before order across separate operations. Clients may not set this value. It is represented in RFC3339 form and is in UTC.\n\nPopulated by the system. Read-only. Null for lists. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata"
                        },
                        "deletionGracePeriodSeconds": {
                            "description": "Number of seconds allowed for this object to gracefully terminate before it will be removed from the system. Only set when deletionTimestamp is also set. May only be shortened. Read-only.",
                            "format": "int64",
                            "type": "integer"
                        },
                        "deletionTimestamp": {
                            "allOf": [
                                {
                                    "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.Time"
                                }
                            ],
                            "description": "DeletionTimestamp is RFC 3339 date and time at which this resource will be deleted. This field is set by the server when a graceful deletion is requested by the user, and is not directly settable by a client. The resource is expected to be deleted (no longer visible from resource lists, and not reachable by name) after the time in this field, once the finalizers list is empty. As long as the finalizers list contains items, deletion is blocked. Once the deletionTimestamp is set, this value may not be unset or be set further into the future, although it may be shortened or the resource may be deleted prior to this time. For example, a user may request that a pod is deleted in 30 seconds. The Kubelet will react by sending a graceful termination signal to the containers in the pod. After that 30 seconds, the Kubelet will send a hard termination signal (SIGKILL) to the container and after cleanup, remove the pod from the API. In the presence of network partitions, this object may still exist after this timestamp, until an administrator or automated process can determine the resource is fully terminated. If not set, graceful deletion of the object has not been requested.\n\nPopulated by the system when a graceful deletion is requested. Read-only. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata"
                        },
                        "finalizers": {
                            "description": "Must be empty before the object is deleted from the registry. Each entry is an identifier for the responsible component that will remove the entry from the list. If the deletionTimestamp of the object is non-nil, entries in this list can only be removed. Finalizers may be processed and removed in any order.  Order is NOT enforced because it introduces significant risk of stuck finalizers. finalizers is a shared field, any actor with permission can reorder it. If the finalizer list is processed in order, then this can lead to a situation in which the component responsible for the first finalizer in the list is waiting for a signal (field value, external system, or other) produced by a component responsible for a finalizer later in the list, resulting in a deadlock. Without enforced ordering finalizers are free to order amongst themselves and are not vulnerable to ordering changes in the list.",
                            "items": {
                                "default": "",
                                "type": "string"
                            },
                            "type": "array",
                            "x-kubernetes-patch-strategy": "merge"
                        },
                        "generateName": {
                            "description": "GenerateName is an optional prefix, used by the server, to generate a unique name ONLY IF the Name field has not been provided. If this field is used, the name returned to the client will be different than the name passed. This value will also be combined with a unique suffix. The provided value has the same validation rules as the Name field, and may be truncated by the length of the suffix required to make the value unique on the server.\n\nIf this field is specified and the generated name exists, the server will return a 409.\n\nApplied only if Name is not specified. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#idempotency",
                            "type": "string"
                        },
                        "generation": {
                            "description": "A sequence number representing a specific generation of the desired state. Populated by the system. Read-only.",
                            "format": "int64",
                            "type": "integer"
                        },
                        "labels": {
                            "additionalProperties": {
                                "default": "",
                                "type": "string"
                            },
                            "description": "Map of string keys and values that can be used to organize and categorize (scope and select) objects. May match selectors of replication controllers and services. More info: http://kubernetes.io/docs/user-guide/labels",
                            "type": "object"
                        },
                        "managedFields": {
                            "description": "ManagedFields maps workflow-id and version to the set of fields that are managed by that workflow. This is mostly for internal housekeeping, and users typically shouldn't need to set or understand this field. A workflow can be the user's name, a controller's name, or the name of a specific apply path like \"ci-cd\". The set of fields is always in the version that the workflow used when modifying the object.",
                            "items": {
                                "allOf": [
                                    {
                                        "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ManagedFieldsEntry"
                                    }
                                ],
                                "default": {}
                            },
                            "type": "array"
                        },
                        "name": {
                            "description": "Name must be unique within a namespace. Is required when creating resources, although some resources may allow a client to request the generation of an appropriate name automatically. Name is primarily intended for creation idempotence and configuration definition. Cannot be updated. More info: http://kubernetes.io/docs/user-guide/identifiers#names",
                            "type": "string"
                        },
                        "namespace": {
                            "description": "Namespace defines the space within which each name must be unique. An empty namespace is equivalent to the \"default\" namespace, but \"default\" is the canonical representation. Not all objects are required to be scoped to a namespace - the value of this field for those objects will be empty.\n\nMust be a DNS_LABEL. Cannot be updated. More info: http://kubernetes.io/docs/user-guide/namespaces",
                            "type": "string"
                        },
                        "ownerReferences": {
                            "description": "List of objects depended by this object. If ALL objects in the list have been deleted, this object will be garbage collected. If this object is managed by a controller, then an entry in this list will point to this controller, with the controller field set to true. There cannot be more than one managing controller.",
                            "items": {
                                "allOf": [
                                    {
                                        "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.OwnerReference"
                                    }
                                ],
                                "default": {}
                            },
                            "type": "array",
                            "x-kubernetes-patch-merge-key": "uid",
                            "x-kubernetes-patch-strategy": "merge"
                        },
                        "resourceVersion": {
                            "description": "An opaque value that represents the internal version of this object that can be used by clients to determine when objects have changed. May be used for optimistic concurrency, change detection, and the watch operation on a resource or set of resources. Clients must treat these values as opaque and passed unmodified back to the server. They may only be valid for a particular resource or set of resources.\n\nPopulated by the system. Read-only. Value must be treated as opaque by clients and . More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#concurrency-control-and-consistency",
                            "type": "string"
                        },
                        "selfLink": {
                            "description": "Deprecated: selfLink is a legacy read-only field that is no longer populated by the system.",
                            "type": "string"
                        },
                        "uid": {
                            "description": "UID is the unique in time and space value for this object. It is typically generated by the server on successful creation of a resource and is not allowed to change on PUT operations.\n\nPopulated by the system. Read-only. More info: http://kubernetes.io/docs/user-guide/identifiers#uids",
                            "type": "string"
                        }
                    },
                    "type": "object"
                },
                "io.k8s.apimachinery.pkg.apis.meta.v1.OwnerReference": {
                    "description": "OwnerReference contains enough information to let you identify an owning object. An owning object must be in the same namespace as the dependent, or be cluster-scoped, so there is no namespace field.",
                    "properties": {
                        "apiVersion": {
                            "default": "",
                            "description": "API version of the referent.",
                            "type": "string"
                        },
                        "blockOwnerDeletion": {
                            "description": "If true, AND if the owner has the \"foregroundDeletion\" finalizer, then the owner cannot be deleted from the key-value store until this reference is removed. See https://kubernetes.io/docs/concepts/architecture/garbage-collection/#foreground-deletion for how the garbage collector interacts with this field and enforces the foreground deletion. Defaults to false. To set this field, a user needs \"delete\" permission of the owner, otherwise 422 (Unprocessable Entity) will be returned.",
                            "type": "boolean"
                        },
                        "controller": {
                            "description": "If true, this reference points to the managing controller.",
                            "type": "boolean"
                        },
                        "kind": {
                            "default": "",
                            "description": "Kind of the referent. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds",
                            "type": "string"
                        },
                        "name": {
                            "default": "",
                            "description": "Name of the referent. More info: http://kubernetes.io/docs/user-guide/identifiers#names",
                            "type": "string"
                        },
                        "uid": {
                            "default": "",
                            "description": "UID of the referent. More info: http://kubernetes.io/docs/user-guide/identifiers#uids",
                            "type": "string"
                        }
                    },
                    "required": [
                        "apiVersion",
                        "kind",
                        "name",
                        "uid"
                    ],
                    "type": "object",
                    "x-kubernetes-map-type": "atomic"
                },
                "io.k8s.apimachinery.pkg.apis.meta.v1.Patch": {
                    "description": "Patch is provided to give a concrete name and type to the Kubernetes PATCH request body.",
                    "type": "object"
                },
                "io.k8s.apimachinery.pkg.apis.meta.v1.Preconditions": {
                    "description": "Preconditions must be fulfilled before an operation (update, delete, etc.) is carried out.",
                    "properties": {
                        "resourceVersion": {
                            "description": "Specifies the target ResourceVersion",
                            "type": "string"
                        },
                        "uid": {
                            "description": "Specifies the target UID.",
                            "type": "string"
                        }
                    },
                    "type": "object"
                },
                "io.k8s.apimachinery.pkg.apis.meta.v1.Status": {
                    "description": "Status is a return value for calls that don't return other objects.",
                    "properties": {
                        "apiVersion": {
                            "description": "APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources",
                            "type": "string"
                        },
                        "code": {
                            "description": "Suggested HTTP return code for this status, 0 if not set.",
                            "format": "int32",
                            "type": "integer"
                        },
                        "details": {
                            "allOf": [
                                {
                                    "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.StatusDetails"
                                }
                            ],
                            "description": "Extended data associated with the reason.  Each reason may define its own extended details. This field is optional and the data returned is not guaranteed to conform to any schema except that defined by the reason type."
                        },
                        "kind": {
                            "description": "Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds",
                            "type": "string"
                        },
                        "message": {
                            "description": "A human-readable description of the status of this operation.",
                            "type": "string"
                        },
                        "metadata": {
                            "allOf": [
                                {
                                    "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ListMeta"
                                }
                            ],
                            "default": {},
                            "description": "Standard list metadata. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds"
                        },
                        "reason": {
                            "description": "A machine-readable description of why this operation is in the \"Failure\" status. If this value is empty there is no information available. A Reason clarifies an HTTP status code but does not override it.",
                            "type": "string"
                        },
                        "status": {
                            "description": "Status of the operation. One of: \"Success\" or \"Failure\". More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#spec-and-status",
                            "type": "string"
                        }
                    },
                    "type": "object"
                },
                "io.k8s.apimachinery.pkg.apis.meta.v1.StatusCause": {
                    "description": "StatusCause provides more information about an api.Status failure, including cases when multiple errors are encountered.",
                    "properties": {
                        "field": {
                            "description": "The field of the resource that has caused this error, as named by its JSON serialization. May include dot and postfix notation for nested attributes. Arrays are zero-indexed.  Fields may appear more than once in an array of causes due to fields having multiple errors. Optional.\n\nExamples:\n  \"name\" - the field \"name\" on the current resource\n  \"items[0].name\" - the field \"name\" on the first array entry in \"items\"",
                            "type": "string"
                        },
                        "message": {
                            "description": "A human-readable description of the cause of the error.  This field may be presented as-is to a reader.",
                            "type": "string"
                        },
                        "reason": {
                            "description": "A machine-readable description of the cause of the error. If this value is empty there is no information available.",
                            "type": "string"
                        }
                    },
                    "type": "object"
                },
                "io.k8s.apimachinery.pkg.apis.meta.v1.StatusDetails": {
                    "description": "StatusDetails is a set of additional properties that MAY be set by the server to provide additional information about a response. The Reason field of a Status object defines what attributes will be set. Clients must ignore fields that do not match the defined type of each attribute, and should assume that any attribute may be empty, invalid, or under defined.",
                    "properties": {
                        "causes": {
                            "description": "The Causes array includes more details associated with the StatusReason failure. Not all StatusReasons may provide detailed causes.",
                            "items": {
                                "allOf": [
                                    {
                                        "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.StatusCause"
                                    }
                                ],
                                "default": {}
                            },
                            "type": "array"
                        },
                        "group": {
                            "description": "The group attribute of the resource associated with the status StatusReason.",
                            "type": "string"
                        },
                        "kind": {
                            "description": "The kind attribute of the resource associated with the status StatusReason. On some operations may differ from the requested resource Kind. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds",
                            "type": "string"
                        },
                        "name": {
                            "description": "The name attribute of the resource associated with the status StatusReason (when there is a single name which can be described).",
                            "type": "string"
                        },
                        "retryAfterSeconds": {
                            "description": "If specified, the time in seconds before the operation should be retried. Some errors may indicate the client must take an alternate action - for those errors this field may indicate how long to wait before taking the alternate action.",
                            "format": "int32",
                            "type": "integer"
                        },
                        "uid": {
                            "description": "UID of the resource. (when there is a single resource which can be described). More info: http://kubernetes.io/docs/user-guide/identifiers#uids",
                            "type": "string"
                        }
                    },
                    "type": "object"
                },
                "io.k8s.apimachinery.pkg.apis.meta.v1.Time": {
                    "description": "Time is a wrapper around time.Time which supports correct marshaling to YAML and JSON.  Wrappers are provided for many of the factory methods that the time package offers.",
                    "format": "date-time",
                    "type": "string"
                }
            }
        },
        "paths": {}
    }

    crd_files = sorted(
        Path("assets/crd/").glob("*.yaml"),
        key=lambda f: (CRD_ORDER.get(f.name, len(CRD_ORDER)), f.name),
    )

    for f in crd_files:
        contents = yaml.load(f)

        spec = contents.get("spec")

        group = spec.get("group", "").lower().replace(".", "-")
        kind = spec.get("names", {}).get("kind", "").lower()
        plural = spec.get("names", {}).get("plural", "")

        if not all((group, kind, plural)):
            print(f"missing group or kind data, cannot continue with file '{f}'")

            continue

        group_slug = group.replace("-", ".")
        group_camel = " ".join(group.split("-")).title().replace(" ", "")

        versions = spec.get("versions", [])
        if not versions:
            print(f"no version data, cannot continue with file '{f}'")

            continue

        for version_spec in spec.get("versions", []):
            version = version_spec.get("name", "")

            if not version:
                print(
                    f"cannot parse version for group '{group}', kind '{kind}'"
                    f", cannot continue with file '{f}'"
                )

                break

            object_name = f"{group}.{kind.lower()}.{version.lower()}"
            object_list_name = f"{group}.{kind.lower()}List.{version.lower()}"

            if out.get("components", {}).get("schemas", {}).get(object_name, ""):
                print(
                    f"data already exists for group '{group}', kind '{kind}', version '{version}'"
                    f", cannot continue with file '{f}'"
                )

                break

            properties = version_spec.get("schema", {}).get("openAPIV3Schema", {})

            if not properties:
                print(
                    f"no schema data for group '{group}', kind '{kind}', version '{version}'"
                    f", cannot continue with file '{f}'"
                )

                break

            description = properties.pop("description", "")

            out["components"]["schemas"][object_name] = {
                "description": description,
                "properties": properties.get("properties", {}),
                "type": "object",
                "x-kubernetes-gvk": {
                    "group": group,
                    "version": version,
                    "kind": kind,
                }
            }

            out["components"]["schemas"][object_list_name] = {
                "description": f"a list of {object_name} resources",
                "properties": {
                    "apiVersion": {
                        "description": "APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources",
                        "type": "string"
                    },
                    "items": {
                        "description": f"List of {plural}. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md",
                        "items": {
                            "$ref": f"#/components/schemas/{object_name}"
                        },
                        "type": "array"
                    },
                    "kind": {
                        "description": "Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds",
                        "type": "string"
                    },
                    "metadata": {
                        "allOf": [
                            {
                                "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ListMeta"
                            }
                        ],
                        "description": "Standard list metadata. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds"
                    }
                },
                "type": "object",
                "required": [
                    "items"
                ],
                "x-kubernetes-gvk": {
                    "group": group,
                    "version": version,
                    "kind": f"{kind}List",
                }
            }

            out["paths"][f"/apis/{group_slug}/{version}/{plural}"] = {
                "get": {
                    "description": f"list objects of kind {kind.title()}",
                    "operationId": f"list{group_camel}{version.title()}{kind.title()}ForAllNamespaces",
                    "responses": {
                        "200": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_list_name}"
                                    }
                                },
                                "application/yaml": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_list_name}"
                                    }
                                }
                            },
                            "description": "OK"
                        },
                        "401": {
                            "description": "Unauthorized"
                        }
                    },
                    "tags": [],
                },
                "parameters": [
                    {
                        "description": "allowWatchBookmarks requests watch events with type \"BOOKMARK\". Servers that do not implement bookmarks may ignore this flag and bookmarks are sent at the server's discretion. Clients should not assume bookmarks are returned at any specific interval, nor may they assume the server will send any BOOKMARK event during a session. If this is not a watch, this field is ignored.",
                        "in": "query",
                        "name": "allowWatchBookmarks",
                        "schema": {
                            "type": "boolean",
                            "uniqueItems": True
                        }
                    },
                    {
                        "description": "The continue option should be set when retrieving more results from the server. Since this value is server defined, clients may only use the continue value from a previous query result with identical query parameters (except for the value of continue) and the server may reject a continue value it does not recognize. If the specified continue value is no longer valid whether due to expiration (generally five to fifteen minutes) or a configuration change on the server, the server will respond with a 410 ResourceExpired error together with a continue token. If the client needs a consistent list, it must restart their list without the continue field. Otherwise, the client may send another list request with the token received with the 410 error, the server will respond with a list starting from the next key, but from the latest snapshot, which is inconsistent from the previous list results - objects that are created, modified, or deleted after the first list request will be included in the response, as long as their keys are after the \"next key\".\n\nThis field is not supported when watch is true. Clients may start a watch from the last resourceVersion value returned by the server and not miss any modifications.",
                        "in": "query",
                        "name": "continue",
                        "schema": {
                            "type": "string",
                            "uniqueItems": True
                        }
                    },
                    {
                        "description": "A selector to restrict the list of returned objects by their fields. Defaults to everything.",
                        "in": "query",
                        "name": "fieldSelector",
                        "schema": {
                            "type": "string",
                            "uniqueItems": True
                        }
                    },
                    {
                        "description": "A selector to restrict the list of returned objects by their labels. Defaults to everything.",
                        "in": "query",
                        "name": "labelSelector",
                        "schema": {
                            "type": "string",
                            "uniqueItems": True
                        }
                    },
                    {
                        "description": "limit is a maximum number of responses to return for a list call. If more items exist, the server will set the `continue` field on the list metadata to a value that can be used with the same initial query to retrieve the next set of results. Setting a limit may return fewer than the requested amount of items (up to zero items) in the event all requested objects are filtered out and clients should only use the presence of the continue field to determine whether more results are available. Servers may choose not to support the limit argument and will return all of the available results. If limit is specified and the continue field is empty, clients may assume that no more results are available. This field is not supported if watch is true.\n\nThe server guarantees that the objects returned when using continue will be identical to issuing a single list call without a limit - that is, no objects created, modified, or deleted after the first request is issued will be included in any subsequent continued requests. This is sometimes referred to as a consistent snapshot, and ensures that a client that is using limit to receive smaller chunks of a very large result can ensure they see all possible objects. If objects are updated during a chunked list the version of the object that was present at the time the first list result was calculated is returned.",
                        "in": "query",
                        "name": "limit",
                        "schema": {
                            "type": "integer",
                            "uniqueItems": True
                        }
                    },
                    {
                        "description": "If 'true', then the output is pretty printed.",
                        "in": "query",
                        "name": "pretty",
                        "schema": {
                            "type": "string",
                            "uniqueItems": True
                        }
                    },
                    {
                        "description": "resourceVersion sets a constraint on what resource versions a request may be served from. See https://kubernetes.io/docs/reference/using-api/api-concepts/#resource-versions for details.\n\nDefaults to unset",
                        "in": "query",
                        "name": "resourceVersion",
                        "schema": {
                            "type": "string",
                            "uniqueItems": True
                        }
                    },
                    {
                        "description": "resourceVersionMatch determines how resourceVersion is applied to list calls. It is highly recommended that resourceVersionMatch be set for list calls where resourceVersion is set See https://kubernetes.io/docs/reference/using-api/api-concepts/#resource-versions for details.\n\nDefaults to unset",
                        "in": "query",
                        "name": "resourceVersionMatch",
                        "schema": {
                            "type": "string",
                            "uniqueItems": True
                        }
                    },
                    {
                        "description": "Timeout for the list/watch call. This limits the duration of the call, regardless of any activity or inactivity.",
                        "in": "query",
                        "name": "timeoutSeconds",
                        "schema": {
                            "type": "integer",
                            "uniqueItems": True
                        }
                    },
                    {
                        "description": "Watch for changes to the described resources and return them as a stream of add, update, and remove notifications. Specify resourceVersion.",
                        "in": "query",
                        "name": "watch",
                        "schema": {
                            "type": "boolean",
                            "uniqueItems": True
                        }
                    }
                ]
            }

            out["paths"][f"/apis/{group_slug}/{version}/namespaces/{{namespace}}/{plural}"] = {
                "delete": {
                    "description": f"delete collection of {kind.title()}",
                    "operationId": f"delete{group_camel}{version.title()}CollectionNamespaced{kind.title()}",
                    "parameters": [
                        {
                            "description": "allowWatchBookmarks requests watch events with type \"BOOKMARK\". Servers that do not implement bookmarks may ignore this flag and bookmarks are sent at the server's discretion. Clients should not assume bookmarks are returned at any specific interval, nor may they assume the server will send any BOOKMARK event during a session. If this is not a watch, this field is ignored.",
                            "in": "query",
                            "name": "allowWatchBookmarks",
                            "schema": {
                                "type": "boolean",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "The continue option should be set when retrieving more results from the server. Since this value is server defined, clients may only use the continue value from a previous query result with identical query parameters (except for the value of continue) and the server may reject a continue value it does not recognize. If the specified continue value is no longer valid whether due to expiration (generally five to fifteen minutes) or a configuration change on the server, the server will respond with a 410 ResourceExpired error together with a continue token. If the client needs a consistent list, it must restart their list without the continue field. Otherwise, the client may send another list request with the token received with the 410 error, the server will respond with a list starting from the next key, but from the latest snapshot, which is inconsistent from the previous list results - objects that are created, modified, or deleted after the first list request will be included in the response, as long as their keys are after the \"next key\".\n\nThis field is not supported when watch is true. Clients may start a watch from the last resourceVersion value returned by the server and not miss any modifications.",
                            "in": "query",
                            "name": "continue",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "A selector to restrict the list of returned objects by their fields. Defaults to everything.",
                            "in": "query",
                            "name": "fieldSelector",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "A selector to restrict the list of returned objects by their labels. Defaults to everything.",
                            "in": "query",
                            "name": "labelSelector",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "limit is a maximum number of responses to return for a list call. If more items exist, the server will set the `continue` field on the list metadata to a value that can be used with the same initial query to retrieve the next set of results. Setting a limit may return fewer than the requested amount of items (up to zero items) in the event all requested objects are filtered out and clients should only use the presence of the continue field to determine whether more results are available. Servers may choose not to support the limit argument and will return all of the available results. If limit is specified and the continue field is empty, clients may assume that no more results are available. This field is not supported if watch is true.\n\nThe server guarantees that the objects returned when using continue will be identical to issuing a single list call without a limit - that is, no objects created, modified, or deleted after the first request is issued will be included in any subsequent continued requests. This is sometimes referred to as a consistent snapshot, and ensures that a client that is using limit to receive smaller chunks of a very large result can ensure they see all possible objects. If objects are updated during a chunked list the version of the object that was present at the time the first list result was calculated is returned.",
                            "in": "query",
                            "name": "limit",
                            "schema": {
                                "type": "integer",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "resourceVersion sets a constraint on what resource versions a request may be served from. See https://kubernetes.io/docs/reference/using-api/api-concepts/#resource-versions for details.\n\nDefaults to unset",
                            "in": "query",
                            "name": "resourceVersion",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "resourceVersionMatch determines how resourceVersion is applied to list calls. It is highly recommended that resourceVersionMatch be set for list calls where resourceVersion is set See https://kubernetes.io/docs/reference/using-api/api-concepts/#resource-versions for details.\n\nDefaults to unset",
                            "in": "query",
                            "name": "resourceVersionMatch",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "Timeout for the list/watch call. This limits the duration of the call, regardless of any activity or inactivity.",
                            "in": "query",
                            "name": "timeoutSeconds",
                            "schema": {
                                "type": "integer",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "Watch for changes to the described resources and return them as a stream of add, update, and remove notifications. Specify resourceVersion.",
                            "in": "query",
                            "name": "watch",
                            "schema": {
                                "type": "boolean",
                                "uniqueItems": True
                            }
                        }
                    ],
                    "responses": {
                        "200": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.Status"
                                    }
                                },
                                "application/yaml": {
                                    "schema": {
                                        "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.Status"
                                    }
                                }
                            },
                            "description": "OK"
                        },
                        "401": {
                            "description": "Unauthorized"
                        }
                    },
                    "tags": [],
                },
                "get": {
                    "description": f"list objects of kind {kind.title()}",
                    "operationId": f"list{group_camel}{version.title()}Namespaced{kind.title()}",
                    "parameters": [
                        {
                            "description": "allowWatchBookmarks requests watch events with type \"BOOKMARK\". Servers that do not implement bookmarks may ignore this flag and bookmarks are sent at the server's discretion. Clients should not assume bookmarks are returned at any specific interval, nor may they assume the server will send any BOOKMARK event during a session. If this is not a watch, this field is ignored.",
                            "in": "query",
                            "name": "allowWatchBookmarks",
                            "schema": {
                                "type": "boolean",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "The continue option should be set when retrieving more results from the server. Since this value is server defined, clients may only use the continue value from a previous query result with identical query parameters (except for the value of continue) and the server may reject a continue value it does not recognize. If the specified continue value is no longer valid whether due to expiration (generally five to fifteen minutes) or a configuration change on the server, the server will respond with a 410 ResourceExpired error together with a continue token. If the client needs a consistent list, it must restart their list without the continue field. Otherwise, the client may send another list request with the token received with the 410 error, the server will respond with a list starting from the next key, but from the latest snapshot, which is inconsistent from the previous list results - objects that are created, modified, or deleted after the first list request will be included in the response, as long as their keys are after the \"next key\".\n\nThis field is not supported when watch is true. Clients may start a watch from the last resourceVersion value returned by the server and not miss any modifications.",
                            "in": "query",
                            "name": "continue",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "A selector to restrict the list of returned objects by their fields. Defaults to everything.",
                            "in": "query",
                            "name": "fieldSelector",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "A selector to restrict the list of returned objects by their labels. Defaults to everything.",
                            "in": "query",
                            "name": "labelSelector",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "limit is a maximum number of responses to return for a list call. If more items exist, the server will set the `continue` field on the list metadata to a value that can be used with the same initial query to retrieve the next set of results. Setting a limit may return fewer than the requested amount of items (up to zero items) in the event all requested objects are filtered out and clients should only use the presence of the continue field to determine whether more results are available. Servers may choose not to support the limit argument and will return all of the available results. If limit is specified and the continue field is empty, clients may assume that no more results are available. This field is not supported if watch is true.\n\nThe server guarantees that the objects returned when using continue will be identical to issuing a single list call without a limit - that is, no objects created, modified, or deleted after the first request is issued will be included in any subsequent continued requests. This is sometimes referred to as a consistent snapshot, and ensures that a client that is using limit to receive smaller chunks of a very large result can ensure they see all possible objects. If objects are updated during a chunked list the version of the object that was present at the time the first list result was calculated is returned.",
                            "in": "query",
                            "name": "limit",
                            "schema": {
                                "type": "integer",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "resourceVersion sets a constraint on what resource versions a request may be served from. See https://kubernetes.io/docs/reference/using-api/api-concepts/#resource-versions for details.\n\nDefaults to unset",
                            "in": "query",
                            "name": "resourceVersion",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "resourceVersionMatch determines how resourceVersion is applied to list calls. It is highly recommended that resourceVersionMatch be set for list calls where resourceVersion is set See https://kubernetes.io/docs/reference/using-api/api-concepts/#resource-versions for details.\n\nDefaults to unset",
                            "in": "query",
                            "name": "resourceVersionMatch",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "Timeout for the list/watch call. This limits the duration of the call, regardless of any activity or inactivity.",
                            "in": "query",
                            "name": "timeoutSeconds",
                            "schema": {
                                "type": "integer",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "Watch for changes to the described resources and return them as a stream of add, update, and remove notifications. Specify resourceVersion.",
                            "in": "query",
                            "name": "watch",
                            "schema": {
                                "type": "boolean",
                                "uniqueItems": True
                            }
                        }
                    ],
                    "responses": {
                        "200": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_list_name}"
                                    }
                                },
                                "application/yaml": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_list_name}"
                                    }
                                }
                            },
                            "description": "OK"
                        },
                        "401": {
                            "description": "Unauthorized"
                        }
                    },
                    "tags": [],
                },
                "post": {
                    "description": f"create a {kind.title()}",
                    "operationId": f"create{group_camel}{version.title()}Namespaced{kind.title()}",
                    "parameters": [
                        {
                            "description": "When present, indicates that modifications should not be persisted. An invalid or unrecognized dryRun directive will result in an error response and no further processing of the request. Valid values are: - All: all dry run stages will be processed",
                            "in": "query",
                            "name": "dryRun",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "fieldManager is a name associated with the actor or entity that is making these changes. The value must be less than or 128 characters long, and only contain printable characters, as defined by https://golang.org/pkg/unicode/#IsPrint.",
                            "in": "query",
                            "name": "fieldManager",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "fieldValidation instructs the server on how to handle objects in the request (POST/PUT/PATCH) containing unknown or duplicate fields, provided that the `ServerSideFieldValidation` feature gate is also enabled. Valid values are: - Ignore: This will ignore any unknown fields that are silently dropped from the object, and will ignore all but the last duplicate field that the decoder encounters. This is the default behavior prior to v1.23 and is the default behavior when the `ServerSideFieldValidation` feature gate is disabled. - Warn: This will send a warning via the standard warning response header for each unknown field that is dropped from the object, and for each duplicate field that is encountered. The request will still succeed if there are no other errors, and will only persist the last of any duplicate fields. This is the default when the `ServerSideFieldValidation` feature gate is enabled. - Strict: This will fail the request with a BadRequest error if any unknown fields would be dropped from the object, or if any duplicate fields are present. The error returned from the server will contain all unknown and duplicate fields encountered.",
                            "in": "query",
                            "name": "fieldValidation",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        }
                    ],
                    "requestBody": {
                        "content": {
                            "application/json": {
                                "schema": {
                                    "$ref": f"#/components/schemas/{object_name}"
                                }
                            },
                            "application/yaml": {
                                "schema": {
                                    "$ref": f"#/components/schemas/{object_name}"
                                }
                            }
                        }
                    },
                    "responses": {
                        "200": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_name}"
                                    }
                                },
                                "application/yaml": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_name}"
                                    }
                                }
                            },
                            "description": "OK"
                        },
                        "201": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_name}"
                                    }
                                },
                                "application/yaml": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_name}"
                                    }
                                }
                            },
                            "description": "Created"
                        },
                        "202": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_name}"
                                    }
                                },
                                "application/yaml": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_name}"
                                    }
                                }
                            },
                            "description": "Accepted"
                        },
                        "401": {
                            "description": "Unauthorized"
                        }
                    },
                    "tags": [],
                },
                "parameters": [
                    {
                        "description": "object name and auth scope, such as for teams and projects",
                        "in": "path",
                        "name": "namespace",
                        "required": True,
                        "schema": {
                            "type": "string",
                            "uniqueItems": True
                        }
                    },
                    {
                        "description": "If 'true', then the output is pretty printed.",
                        "in": "query",
                        "name": "pretty",
                        "schema": {
                            "type": "string",
                            "uniqueItems": True
                        }
                    }
                ]
            }

            out["paths"][f"/apis/{group_slug}/{version}/namespaces/{{namespace}}/{plural}/{{name}}"] = {
                "delete": {
                    "description": f"delete a {kind.title()}",
                    "operationId": f"delete{group_camel}{version.title()}Namespaced{kind.title()}",
                    "parameters": [
                        {
                            "description": "When present, indicates that modifications should not be persisted. An invalid or unrecognized dryRun directive will result in an error response and no further processing of the request. Valid values are: - All: all dry run stages will be processed",
                            "in": "query",
                            "name": "dryRun",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "The duration in seconds before the object should be deleted. Value must be non-negative integer. The value zero indicates delete immediately. If this value is nil, the default grace period for the specified type will be used. Defaults to a per object value if not specified. zero means delete immediately.",
                            "in": "query",
                            "name": "gracePeriodSeconds",
                            "schema": {
                                "type": "integer",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "Deprecated: please use the PropagationPolicy, this field will be deprecated in 1.7. Should the dependent objects be orphaned. If true/false, the \"orphan\" finalizer will be added to/removed from the object's finalizers list. Either this field or PropagationPolicy may be set, but not both.",
                            "in": "query",
                            "name": "orphanDependents",
                            "schema": {
                                "type": "boolean",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "Whether and how garbage collection will be performed. Either this field or OrphanDependents may be set, but not both. The default policy is decided by the existing finalizer set in the metadata.finalizers and the resource-specific default policy. Acceptable values are: 'Orphan' - orphan the dependents; 'Background' - allow the garbage collector to delete the dependents in the background; 'Foreground' - a cascading policy that deletes all dependents in the foreground.",
                            "in": "query",
                            "name": "propagationPolicy",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        }
                    ],
                    "requestBody": {
                        "content": {
                            "application/json": {
                                "schema": {
                                    "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.DeleteOptions"
                                }
                            },
                            "application/yaml": {
                                "schema": {
                                    "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.DeleteOptions"
                                }
                            }
                        }
                    },
                    "responses": {
                        "200": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.Status"
                                    }
                                },
                                "application/yaml": {
                                    "schema": {
                                        "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.Status"
                                    }
                                }
                            },
                            "description": "OK"
                        },
                        "202": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.Status"
                                    }
                                },
                                "application/yaml": {
                                    "schema": {
                                        "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.Status"
                                    }
                                }
                            },
                            "description": "Accepted"
                        },
                        "401": {
                            "description": "Unauthorized"
                        }
                    },
                    "tags": [],
                },
                "get": {
                    "description": f"read the specified {kind.title()}",
                    "operationId": f"read{group_camel}{version.title()}Namespaced{kind.title()}",
                    "parameters": [
                        {
                            "description": "resourceVersion sets a constraint on what resource versions a request may be served from. See https://kubernetes.io/docs/reference/using-api/api-concepts/#resource-versions for details.\n\nDefaults to unset",
                            "in": "query",
                            "name": "resourceVersion",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        }
                    ],
                    "responses": {
                        "200": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_name}"
                                    }
                                },
                                "application/yaml": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_name}"
                                    }
                                }
                            },
                            "description": "OK"
                        },
                        "401": {
                            "description": "Unauthorized"
                        }
                    },
                    "tags": [],
                },
                "patch": {
                    "description": f"partially update the specified {kind.title()}",
                    "operationId": f"patch{group_camel}{version.title()}Namespaced{kind.title()}",
                    "parameters": [
                        {
                            "description": "When present, indicates that modifications should not be persisted. An invalid or unrecognized dryRun directive will result in an error response and no further processing of the request. Valid values are: - All: all dry run stages will be processed",
                            "in": "query",
                            "name": "dryRun",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "fieldManager is a name associated with the actor or entity that is making these changes. The value must be less than or 128 characters long, and only contain printable characters, as defined by https://golang.org/pkg/unicode/#IsPrint.",
                            "in": "query",
                            "name": "fieldManager",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "fieldValidation instructs the server on how to handle objects in the request (POST/PUT/PATCH) containing unknown or duplicate fields, provided that the `ServerSideFieldValidation` feature gate is also enabled. Valid values are: - Ignore: This will ignore any unknown fields that are silently dropped from the object, and will ignore all but the last duplicate field that the decoder encounters. This is the default behavior prior to v1.23 and is the default behavior when the `ServerSideFieldValidation` feature gate is disabled. - Warn: This will send a warning via the standard warning response header for each unknown field that is dropped from the object, and for each duplicate field that is encountered. The request will still succeed if there are no other errors, and will only persist the last of any duplicate fields. This is the default when the `ServerSideFieldValidation` feature gate is enabled. - Strict: This will fail the request with a BadRequest error if any unknown fields would be dropped from the object, or if any duplicate fields are present. The error returned from the server will contain all unknown and duplicate fields encountered.",
                            "in": "query",
                            "name": "fieldValidation",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        }
                    ],
                    "requestBody": {
                        "content": {
                            "application/apply-patch+yaml": {
                                "schema": {
                                    "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.Patch"
                                }
                            },
                            "application/json-patch+json": {
                                "schema": {
                                    "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.Patch"
                                }
                            },
                            "application/merge-patch+json": {
                                "schema": {
                                    "$ref": "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.Patch"
                                }
                            }
                        }
                    },
                    "responses": {
                        "200": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_name}"
                                    }
                                },
                                "application/yaml": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_name}"
                                    }
                                }
                            },
                            "description": "OK"
                        },
                        "401": {
                            "description": "Unauthorized"
                        }
                    },
                    "tags": [],
                },
                "put": {
                    "description": f"replace the specified {kind.title()}",
                    "operationId": f"replace{group_camel}{version.title()}Namespaced{kind.title()}",
                    "parameters": [
                        {
                            "description": "When present, indicates that modifications should not be persisted. An invalid or unrecognized dryRun directive will result in an error response and no further processing of the request. Valid values are: - All: all dry run stages will be processed",
                            "in": "query",
                            "name": "dryRun",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "fieldManager is a name associated with the actor or entity that is making these changes. The value must be less than or 128 characters long, and only contain printable characters, as defined by https://golang.org/pkg/unicode/#IsPrint.",
                            "in": "query",
                            "name": "fieldManager",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        },
                        {
                            "description": "fieldValidation instructs the server on how to handle objects in the request (POST/PUT/PATCH) containing unknown or duplicate fields, provided that the `ServerSideFieldValidation` feature gate is also enabled. Valid values are: - Ignore: This will ignore any unknown fields that are silently dropped from the object, and will ignore all but the last duplicate field that the decoder encounters. This is the default behavior prior to v1.23 and is the default behavior when the `ServerSideFieldValidation` feature gate is disabled. - Warn: This will send a warning via the standard warning response header for each unknown field that is dropped from the object, and for each duplicate field that is encountered. The request will still succeed if there are no other errors, and will only persist the last of any duplicate fields. This is the default when the `ServerSideFieldValidation` feature gate is enabled. - Strict: This will fail the request with a BadRequest error if any unknown fields would be dropped from the object, or if any duplicate fields are present. The error returned from the server will contain all unknown and duplicate fields encountered.",
                            "in": "query",
                            "name": "fieldValidation",
                            "schema": {
                                "type": "string",
                                "uniqueItems": True
                            }
                        }
                    ],
                    "requestBody": {
                        "content": {
                            "application/json": {
                                "schema": {
                                    "$ref": f"#/components/schemas/{object_name}"
                                }
                            },
                            "application/yaml": {
                                "schema": {
                                    "$ref": f"#/components/schemas/{object_name}"
                                }
                            }
                        }
                    },
                    "responses": {
                        "200": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_name}"
                                    }
                                },
                                "application/yaml": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_name}"
                                    }
                                }
                            },
                            "description": "OK"
                        },
                        "201": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_name}"
                                    }
                                },
                                "application/yaml": {
                                    "schema": {
                                        "$ref": f"#/components/schemas/{object_name}"
                                    }
                                }
                            },
                            "description": "Created"
                        },
                        "401": {
                            "description": "Unauthorized"
                        }
                    },
                    "tags": [],
                },
                "parameters": [
                    {
                        "description": f"name of the {kind.title()}",
                        "in": "path",
                        "name": "name",
                        "required": True,
                        "schema": {
                            "type": "string",
                            "uniqueItems": True
                        }
                    },
                    {
                        "description": "object name and auth scope, such as for teams and projects",
                        "in": "path",
                        "name": "namespace",
                        "required": True,
                        "schema": {
                            "type": "string",
                            "uniqueItems": True
                        }
                    },
                    {
                        "description": "If 'true', then the output is pretty printed.",
                        "in": "query",
                        "name": "pretty",
                        "schema": {
                            "type": "string",
                            "uniqueItems": True
                        }
                    }
                ]
            }

    with open("generated/openapi/openapi.json", "w") as f:
        json.dump(out, f, indent=4)


if __name__ == "__main__":
    main()
