package topology_test

import (
	"testing"

	clabernetescontrollerstopology "github.com/clabernetes/clabernetes/controllers/topology"
	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestServiceConforms(t *testing.T) {
	cases := []struct {
		name     string
		existing *k8scorev1.Service
		rendered *k8scorev1.Service
		ownerUID apimachinerytypes.UID
		conforms bool
	}{
		{
			name: "simple",
			existing: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8scorev1.Service{},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: true,
		},
		{
			name: "conforms",
			existing: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"user-provided-global-annotation": "expected-value",
						"someextraannotations":            "extraisok",
					},
					Labels: map[string]string{
						"user-provided-global-label": "expected-value",
						"clabernetes/app":            "clabernetes",
						"someextralabel":             "extraisok",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Spec: k8scorev1.ServiceSpec{
					Ports: []k8scorev1.ServicePort{
						{
							Name:     "telnet-cuz-sekurity",
							Protocol: "TCP",
							Port:     23,
							TargetPort: intstr.IntOrString{
								IntVal: 23,
							},
						},
						{
							Name:     "ssh-for-reasons",
							Protocol: "TCP",
							Port:     22,
							TargetPort: intstr.IntOrString{
								IntVal: 22,
							},
						},
					},
				},
			},
			rendered: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"user-provided-global-annotation": "expected-value",
					},
					Labels: map[string]string{
						"user-provided-global-label": "expected-value",
						"clabernetes/app":            "clabernetes",
					},
				},
				Spec: k8scorev1.ServiceSpec{
					Ports: []k8scorev1.ServicePort{
						{
							Name:     "telnet-cuz-sekurity",
							Protocol: "TCP",
							Port:     23,
							TargetPort: intstr.IntOrString{
								IntVal: 23,
							},
						},
						{
							Name:     "ssh-for-reasons",
							Protocol: "TCP",
							Port:     22,
							TargetPort: intstr.IntOrString{
								IntVal: 22,
							},
						},
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: true,
		},
		{
			name: "bad-selector",
			existing: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Spec: k8scorev1.ServiceSpec{
					Selector: map[string]string{
						"something": "something",
					},
				},
			},
			rendered: &k8scorev1.Service{
				Spec: k8scorev1.ServiceSpec{
					Selector: map[string]string{
						"different": "different",
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "bad-type",
			existing: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Spec: k8scorev1.ServiceSpec{
					Type: "ClusterIP",
				},
			},
			rendered: &k8scorev1.Service{
				Spec: k8scorev1.ServiceSpec{
					Type: "NodePort",
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "bad-port-number",
			existing: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Spec: k8scorev1.ServiceSpec{
					Ports: []k8scorev1.ServicePort{
						{
							Name:     "telnet-cuz-sekurity",
							Protocol: "TCP",
							Port:     23,
							TargetPort: intstr.IntOrString{
								IntVal: 23,
							},
						},
					},
				},
			},
			rendered: &k8scorev1.Service{
				Spec: k8scorev1.ServiceSpec{
					Ports: []k8scorev1.ServicePort{
						{
							Name:     "telnet-cuz-sekurity",
							Protocol: "TCP",
							Port:     99,
							TargetPort: intstr.IntOrString{
								IntVal: 23,
							},
						},
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "no-matching-port-name",
			existing: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Spec: k8scorev1.ServiceSpec{
					Ports: []k8scorev1.ServicePort{
						{
							Name:     "telnet-cuz-sekurity",
							Protocol: "TCP",
							Port:     23,
							TargetPort: intstr.IntOrString{
								IntVal: 23,
							},
						},
					},
				},
			},
			rendered: &k8scorev1.Service{
				Spec: k8scorev1.ServiceSpec{
					Ports: []k8scorev1.ServicePort{
						{
							Name:     "something-else",
							Protocol: "TCP",
							Port:     23,
							TargetPort: intstr.IntOrString{
								IntVal: 23,
							},
						},
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "port-target-mismatch",
			existing: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
				Spec: k8scorev1.ServiceSpec{
					Ports: []k8scorev1.ServicePort{
						{
							Name:     "telnet-cuz-sekurity",
							Protocol: "TCP",
							Port:     23,
							TargetPort: intstr.IntOrString{
								IntVal: 23,
							},
						},
					},
				},
			},
			rendered: &k8scorev1.Service{
				Spec: k8scorev1.ServiceSpec{
					Ports: []k8scorev1.ServicePort{
						{
							Name:     "telnet-cuz-sekurity",
							Protocol: "TCP",
							Port:     23,
							TargetPort: intstr.IntOrString{
								IntVal: 99,
							},
						},
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "missing-clabernetes-global-annotations",
			existing: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"somethingelse": "xyz",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"user-provided-global-annotation": "expected-value",
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "clabernetes-global-annotations-wrong-value",
			existing: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"user-provided-global-annotation": "xyz",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"user-provided-global-annotation": "expected-value",
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "extra-annotations-ok",
			existing: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"somethingelseentirely": "thisisok",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: true,
		},
		{
			name: "missing-clabernetes-labels",
			existing: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"somethingelse": "xyz",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"clabernetes/app": "clabernetes",
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "clabernetes-labels-wrong-value",
			existing: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"clabernetes/app": "xyz",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"clabernetes/app": "clabernetes",
					},
				},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "extra-labels-ok",
			existing: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"somethingelseentirely": "thisisok",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{},
			},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: true,
		},
		{
			name: "bad-owner",
			existing: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("evil-imposter"),
						},
					},
				},
			},
			rendered: &k8scorev1.Service{},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
		{
			name: "multiple-owner",
			existing: &k8scorev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							UID: apimachinerytypes.UID("evil-imposter"),
						},
						{
							UID: apimachinerytypes.UID("clabernetes-testing"),
						},
					},
				},
			},
			rendered: &k8scorev1.Service{},
			ownerUID: apimachinerytypes.UID("clabernetes-testing"),
			conforms: false,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetescontrollerstopology.ServiceConforms(
					testCase.existing,
					testCase.rendered,
					testCase.ownerUID,
				)
				if actual != testCase.conforms {
					clabernetestesthelper.FailOutput(t, testCase.existing, testCase.rendered)
				}
			})
	}
}
