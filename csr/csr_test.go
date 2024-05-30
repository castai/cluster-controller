package csr

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ktest "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	"github.com/castai/cluster-controller/castai"
)

func TestApproveCSR(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	r := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := getClient()
	r.NoError(err)

	cert, err := GetCertificateByNodeName(ctx, client, "gke-csr-cast-pool-ab259afb")
	r.NoError(err)

	err = cert.DeleteCertificate(ctx, client)
	r.NoError(err)

	cert, err = cert.NewCSR(ctx, client)
	r.NoError(err)

	_, err = cert.ApproveCertificate(ctx, client)
	r.NoError(err)
}

func getClient() (*kubernetes.Clientset, error) {
	var kubeconfig string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	return clientset, err
}

func Test_isAutoApproveAllowedForNode(t *testing.T) {
	type args struct {
		tuneMockNode      runtime.Object
		tuneMockErr       error
		subjectCommonName string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "empty node name",
			want: false,
		},
		{
			name: "empty node get response",
			args: args{
				subjectCommonName: "system:node:node1",
			},
			want: false,
		},
		{
			name: "empty node get response",
			args: args{
				subjectCommonName: "system:node:node1",
				tuneMockErr:       fmt.Errorf("error"),
			},
			want: false,
		},
		{
			name: "not CastAI node",
			args: args{
				subjectCommonName: "node1",
				tuneMockNode: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "system:node:node1",
						CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour * 25)),
					},
				},
			},
			want: false,
		},
		{
			name: "not old enough CastAI node",
			args: args{
				subjectCommonName: "system:node:node1",
				tuneMockNode: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							castai.LabelManagedBy: castai.LabelValueManagedByCASTAI,
						},
						CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour)),
						Name:              "node1",
					},
				},
			},
			want: false,
		},
		{
			name: "not proper value of CastAI label",
			args: args{
				subjectCommonName: "system:node:node1",
				tuneMockNode: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour * 25)),
						Name:              "node1",
						Labels: map[string]string{
							castai.LabelManagedBy: "tests",
						},
					},
				},
			},
			want: false,
		},
		{
			name: "true",
			args: args{
				subjectCommonName: "system:node:node1",
				tuneMockNode: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour * 25)),
						Name:              "node1",
						Labels: map[string]string{
							castai.LabelManagedBy: castai.LabelValueManagedByCASTAI,
						},
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			ch := make(chan struct{})

			client.PrependReactor("get", "nodes", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
				close(ch)
				return true, tt.args.tuneMockNode, tt.args.tuneMockErr
			})
			if got, _ := isAutoApproveAllowedForNode(context.Background(), client, tt.args.subjectCommonName); got != tt.want {
				t.Errorf("isAutoApproveAllowedForNode() = %v, want %v", got, tt.want)
			}
		})
	}
}
