/*
Copyright 2021 The KubeVela Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cli

import (
	"context"
	"fmt"

	v1alpha12 "github.com/oam-dev/cluster-gateway/pkg/apis/cluster/v1alpha1"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	v1 "k8s.io/api/core/v1"
	v13 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	errors2 "k8s.io/apimachinery/pkg/api/errors"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types2 "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/oam-dev/kubevela/apis/core.oam.dev/v1alpha1"
	"github.com/oam-dev/kubevela/apis/core.oam.dev/v1beta1"
	"github.com/oam-dev/kubevela/apis/types"
	"github.com/oam-dev/kubevela/pkg/multicluster"
	"github.com/oam-dev/kubevela/pkg/oam"
	"github.com/oam-dev/kubevela/pkg/utils/common"
	errors3 "github.com/oam-dev/kubevela/pkg/utils/errors"
	"github.com/oam-dev/kubevela/references/a/preimport"
)

const (
	// FlagClusterName specifies the cluster name
	FlagClusterName = "name"
)

// ClusterCommandGroup create a group of cluster command
func ClusterCommandGroup(c common.Args) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Manage Clusters",
		Long:  "Manage Clusters",
		Annotations: map[string]string{
			types.TagCommandType: types.TypeSystem,
		},
		// check if cluster-gateway is ready
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if c.Config == nil {
				if err := c.SetConfig(); err != nil {
					return errors.Wrapf(err, "failed to set config for k8s client")
				}
			}
			c.Config.Wrap(multicluster.NewSecretModeMultiClusterRoundTripper)
			c.Client = nil
			preimport.SuppressLogging()
			k8sClient, err := c.GetClient()
			preimport.ResumeLogging()
			if err != nil {
				return errors.Wrapf(err, "failed to get k8s client")
			}
			svc, err := multicluster.GetClusterGatewayService(context.Background(), k8sClient)
			if err != nil {
				return errors.Wrapf(err, "failed to get cluster secret namespace, please ensure cluster gateway is correctly deployed")
			}
			multicluster.ClusterGatewaySecretNamespace = svc.Namespace
			return nil
		},
	}
	cmd.AddCommand(
		NewClusterListCommand(&c),
		NewClusterJoinCommand(&c),
		NewClusterRenameCommand(&c),
		NewClusterDetachCommand(&c),
	)
	return cmd
}

// NewClusterListCommand create cluster list command
func NewClusterListCommand(c *common.Args) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "list managed clusters",
		Long:    "list child clusters managed by KubeVela",
		Args:    cobra.ExactValidArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			secrets := v1.SecretList{}
			if err := c.Client.List(context.Background(), &secrets, client.HasLabels{v1alpha12.LabelKeyClusterCredentialType}, client.InNamespace(multicluster.ClusterGatewaySecretNamespace)); err != nil {
				return errors.Wrapf(err, "failed to get cluster secrets")
			}
			table := newUITable().AddRow("CLUSTER", "TYPE", "ENDPOINT")
			for _, secret := range secrets.Items {
				table.AddRow(secret.Name, secret.GetLabels()[v1alpha12.LabelKeyClusterCredentialType], string(secret.Data["endpoint"]))
			}
			if len(table.Rows) == 1 {
				cmd.Println("No managed cluster found.")
			} else {
				cmd.Println(table.String())
			}
			return nil
		},
	}
	return cmd
}

func ensureClusterNotExists(c client.Client, clusterName string) error {
	secret := &v1.Secret{}
	err := c.Get(context.Background(), types2.NamespacedName{Name: clusterName, Namespace: multicluster.ClusterGatewaySecretNamespace}, secret)
	if err == nil {
		return fmt.Errorf("cluster %s already exists", clusterName)
	}
	if !errors2.IsNotFound(err) {
		return errors.Wrapf(err, "failed to check duplicate cluster secret")
	}
	return nil
}

func ensureResourceTrackerCRDInstalled(c client.Client, clusterName string) error {
	ctx := context.Background()
	remoteCtx := multicluster.ContextWithClusterName(ctx, clusterName)
	crdName := types2.NamespacedName{Name: "resourcetrackers." + v1beta1.Group}
	if err := c.Get(remoteCtx, crdName, &v13.CustomResourceDefinition{}); err != nil {
		if !errors2.IsNotFound(err) {
			return errors.Wrapf(err, "failed to check resourcetracker crd in cluster %s", clusterName)
		}
		crd := &v13.CustomResourceDefinition{}
		if err = c.Get(ctx, crdName, crd); err != nil {
			return errors.Wrapf(err, "failed to get resourcetracker crd in hub cluster")
		}
		crd.ObjectMeta = v12.ObjectMeta{
			Name:        crdName.Name,
			Annotations: crd.Annotations,
			Labels:      crd.Labels,
		}
		if err = c.Create(remoteCtx, crd); err != nil {
			return errors.Wrapf(err, "failed to create resourcetracker crd in cluster %s", clusterName)
		}
	}
	return nil
}

// NewClusterJoinCommand create command to help user join cluster to multicluster management
func NewClusterJoinCommand(c *common.Args) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "join [KUBECONFIG]",
		Short: "join managed cluster",
		Long:  "join managed cluster by kubeconfig",
		Example: "# Join cluster declared in my-child-cluster.kubeconfig\n" +
			"> vela cluster join my-child-cluster.kubeconfig --name example-cluster",
		Args: cobra.ExactValidArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			config, err := clientcmd.LoadFromFile(args[0])
			if err != nil {
				return errors.Wrapf(err, "failed to get kubeconfig")
			}
			if len(config.CurrentContext) == 0 {
				return fmt.Errorf("current-context is not set")
			}
			ctx, ok := config.Contexts[config.CurrentContext]
			if !ok {
				return fmt.Errorf("current-context %s not found", config.CurrentContext)
			}
			cluster, ok := config.Clusters[ctx.Cluster]
			if !ok {
				return fmt.Errorf("cluster %s not found", ctx.Cluster)
			}
			authInfo, ok := config.AuthInfos[ctx.AuthInfo]
			if !ok {
				return fmt.Errorf("authInfo %s not found", ctx.AuthInfo)
			}

			// get ClusterName from flag or config
			clusterName, err := cmd.Flags().GetString(FlagClusterName)
			if err != nil {
				return errors.Wrapf(err, "failed to get cluster name flag")
			}
			if clusterName == "" {
				clusterName = ctx.Cluster
			}
			if clusterName == multicluster.ClusterLocalName {
				return fmt.Errorf("cannot use `%s` as cluster name, it is reserved as the local cluster", multicluster.ClusterLocalName)
			}

			if err := ensureClusterNotExists(c.Client, clusterName); err != nil {
				return errors.Wrapf(err, "cannot use cluster name %s", clusterName)
			}
			var credentialType v1alpha12.CredentialType
			data := map[string][]byte{
				"endpoint": []byte(cluster.Server),
				"ca.crt":   cluster.CertificateAuthorityData,
			}
			if len(authInfo.Token) > 0 {
				credentialType = v1alpha12.CredentialTypeServiceAccountToken
				data["token"] = []byte(authInfo.Token)
			} else {
				credentialType = v1alpha12.CredentialTypeX509Certificate
				data["tls.crt"] = authInfo.ClientCertificateData
				data["tls.key"] = authInfo.ClientKeyData
			}
			secret := &v1.Secret{
				ObjectMeta: v12.ObjectMeta{
					Name:      clusterName,
					Namespace: multicluster.ClusterGatewaySecretNamespace,
					Labels: map[string]string{
						v1alpha12.LabelKeyClusterCredentialType: string(credentialType),
					},
				},
				Type: v1.SecretTypeOpaque,
				Data: data,
			}
			if err := c.Client.Create(context.Background(), secret); err != nil {
				return errors.Wrapf(err, "failed to add cluster to kubernetes")
			}
			if err := ensureResourceTrackerCRDInstalled(c.Client, clusterName); err != nil {
				_ = c.Client.Delete(context.Background(), secret)
				return errors.Wrapf(err, "failed to ensure resourcetracker crd installed in cluster %s", clusterName)
			}
			cmd.Printf("Successfully add cluster %s, endpoint: %s.\n", clusterName, cluster.Server)
			return nil
		},
	}
	cmd.Flags().StringP(FlagClusterName, "n", "", "Specify the cluster name. If empty, it will use the cluster name in config file. Default to be empty.")
	return cmd
}

func getMutableClusterSecret(c client.Client, clusterName string) (*v1.Secret, error) {
	clusterSecret := &v1.Secret{}
	if err := c.Get(context.Background(), types2.NamespacedName{Namespace: multicluster.ClusterGatewaySecretNamespace, Name: clusterName}, clusterSecret); err != nil {
		return nil, errors.Wrapf(err, "failed to find target cluster secret %s", clusterName)
	}
	labels := clusterSecret.GetLabels()
	if labels == nil || labels[v1alpha12.LabelKeyClusterCredentialType] == "" {
		return nil, fmt.Errorf("invalid cluster secret %s: cluster credential type label %s is not set", clusterName, v1alpha12.LabelKeyClusterCredentialType)
	}
	ebs := &v1alpha1.EnvBindingList{}
	if err := c.List(context.Background(), ebs); err != nil {
		return nil, errors.Wrap(err, "failed to find EnvBindings to check clusters")
	}
	errs := errors3.ErrorList{}
	for _, eb := range ebs.Items {
		for _, decision := range eb.Status.ClusterDecisions {
			if decision.Cluster == clusterName {
				errs.Append(fmt.Errorf("application %s/%s (env: %s, envBinding: %s) is currently using cluster %s", eb.Namespace, eb.Labels[oam.LabelAppName], decision.Env, eb.Name, clusterName))
			}
		}
	}
	if errs.HasError() {
		return nil, errors.Wrapf(errs, "cluster %s is in use now", clusterName)
	}
	return clusterSecret, nil
}

// NewClusterRenameCommand create command to help user rename cluster
func NewClusterRenameCommand(c *common.Args) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rename [OLD_NAME] [NEW_NAME]",
		Short: "rename managed cluster",
		Args:  cobra.ExactValidArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldClusterName := args[0]
			newClusterName := args[1]
			if newClusterName == multicluster.ClusterLocalName {
				return fmt.Errorf("cannot use `%s` as cluster name, it is reserved as the local cluster", multicluster.ClusterLocalName)
			}
			clusterSecret, err := getMutableClusterSecret(c.Client, oldClusterName)
			if err != nil {
				return errors.Wrapf(err, "cluster %s is not mutable now", oldClusterName)
			}
			if err := ensureClusterNotExists(c.Client, newClusterName); err != nil {
				return errors.Wrapf(err, "cannot set cluster name to %s", newClusterName)
			}
			if err := c.Client.Delete(context.Background(), clusterSecret); err != nil {
				return errors.Wrapf(err, "failed to rename cluster from %s to %s", oldClusterName, newClusterName)
			}
			clusterSecret.ObjectMeta = v12.ObjectMeta{
				Name:        newClusterName,
				Namespace:   multicluster.ClusterGatewaySecretNamespace,
				Labels:      clusterSecret.Labels,
				Annotations: clusterSecret.Annotations,
			}
			if err := c.Client.Create(context.Background(), clusterSecret); err != nil {
				return errors.Wrapf(err, "failed to rename cluster from %s to %s", oldClusterName, newClusterName)
			}
			cmd.Printf("Rename cluster %s to %s successfully.\n", oldClusterName, newClusterName)
			return nil
		},
	}
	return cmd
}

// NewClusterDetachCommand create command to help user detach existing cluster
func NewClusterDetachCommand(c *common.Args) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "detach [CLUSTER_NAME]",
		Short: "detach managed cluster",
		Args:  cobra.ExactValidArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterName := args[0]
			if clusterName == multicluster.ClusterLocalName {
				return fmt.Errorf("cannot delete `%s` cluster, it is reserved as the local cluster", multicluster.ClusterLocalName)
			}
			clusterSecret, err := getMutableClusterSecret(c.Client, clusterName)
			if err != nil {
				return errors.Wrapf(err, "cluster %s is not mutable now", clusterName)
			}
			if err := c.Client.Delete(context.Background(), clusterSecret); err != nil {
				return errors.Wrapf(err, "failed to detach cluster %s", clusterName)
			}
			cmd.Printf("Detach cluster %s successfully.\n", clusterName)
			return nil
		},
	}
	return cmd
}
