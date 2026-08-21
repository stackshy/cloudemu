package kubernetes

// This file registers the base OKD/OCP *.openshift.io kinds beyond the
// config.openshift.io singletons (which live in openshift.go). Each group is a
// small function returning its resourceDefs; openshiftRegistryDefs concatenates
// them. Kinds are registered as plain CRUD stores here — runtime behavior
// (Route admission, DeploymentConfig rollout, ImageStream status, Project SCC
// annotations, …) is layered on as reconcile hooks separately.
//
// Kinds that are POST-only "request"/"review" verbs or server-computed virtual
// views (ProjectRequest, *Review, ImageStreamTag/ImageStreamImage,
// UserIdentityMapping, AppliedClusterResourceQuota) are intentionally NOT
// registered as stores — advertising them as CRUD would have clients issue
// calls the generic store answers with the wrong semantics. They are added with
// dedicated handlers where needed.

// openshiftAppsDefs registers apps.openshift.io/v1 DeploymentConfig — the
// pre-Deployment workload controller. It scales (spec.replicas) and reports
// status, so it carries both subresources.
func openshiftAppsDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupOSApps, version: "v1", kind: "DeploymentConfig", listKind: "DeploymentConfigList",
			plural: "deploymentconfigs", namespaced: true, hasStatus: true, hasScale: true,
			reconcile: reconcileDeploymentConfig,
		},
	}
}

// openshiftRouteDefs registers route.openshift.io/v1 Route — OpenShift's
// pre-Ingress north-south exposure object. status.ingress admission is a
// reconcile behavior added later.
func openshiftRouteDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupOSRoute, version: "v1", kind: "Route", listKind: "RouteList",
			plural: "routes", namespaced: true, hasStatus: true, reconcile: reconcileRoute,
		},
	}
}

// openshiftBuildDefs registers build.openshift.io/v1 Build and BuildConfig — the
// source-to-image build system's request (BuildConfig) and instance (Build).
func openshiftBuildDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupOSBuild, version: "v1", kind: "BuildConfig", listKind: "BuildConfigList",
			plural: "buildconfigs", namespaced: true, hasStatus: true,
		},
		{
			group: apiGroupOSBuild, version: "v1", kind: "Build", listKind: "BuildList",
			plural: "builds", namespaced: true, hasStatus: true, reconcile: reconcileBuild,
		},
	}
}

// openshiftImageDefs registers image.openshift.io/v1 ImageStream (namespaced,
// status carries the synthesized registry repositories) and Image (the
// cluster-scoped image metadata object). The derived tag/import views
// (imagestreamtags, imagestreamimages, imagestreamimports, imagestreammappings)
// are server-computed and handled separately, not as plain stores.
func openshiftImageDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupOSImage, version: "v1", kind: "ImageStream", listKind: "ImageStreamList",
			plural: "imagestreams", namespaced: true, hasStatus: true, reconcile: reconcileImageStream,
		},
		{
			group: apiGroupOSImage, version: "v1", kind: "Image", listKind: "ImageList",
			plural: "images", namespaced: false,
		},
	}
}

// openshiftProjectDefs registers project.openshift.io/v1 Project — the
// cluster-scoped tenancy object OpenShift layers over a Namespace. (ProjectRequest,
// the self-service create verb, is a POST-only handler added separately.)
func openshiftProjectDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupOSProject, version: "v1", kind: "Project", listKind: "ProjectList",
			plural: "projects", namespaced: false, hasStatus: true, reconcile: reconcileProject,
		},
	}
}

// openshiftUserDefs registers user.openshift.io/v1 User, Group, and Identity —
// all cluster-scoped identity objects. (UserIdentityMapping is a POST-only
// mapping verb, handled separately.)
func openshiftUserDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupOSUser, version: "v1", kind: "User", listKind: "UserList",
			plural: "users", namespaced: false,
		},
		{
			group: apiGroupOSUser, version: "v1", kind: "Group", listKind: "GroupList",
			plural: "groups", namespaced: false,
		},
		{
			group: apiGroupOSUser, version: "v1", kind: "Identity", listKind: "IdentityList",
			plural: "identities", namespaced: false,
		},
	}
}

// openshiftOAuthDefs registers oauth.openshift.io/v1 OAuth registration and
// token objects (all cluster-scoped). These back the OAuth server the oc-login
// flow uses; here they are the persisted stores clients can list/inspect.
func openshiftOAuthDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupOSOAuth, version: "v1", kind: "OAuthClient", listKind: "OAuthClientList",
			plural: "oauthclients", namespaced: false,
		},
		{
			group: apiGroupOSOAuth, version: "v1", kind: "OAuthAccessToken", listKind: "OAuthAccessTokenList",
			plural: "oauthaccesstokens", namespaced: false,
		},
		{
			group: apiGroupOSOAuth, version: "v1", kind: "OAuthAuthorizeToken", listKind: "OAuthAuthorizeTokenList",
			plural: "oauthauthorizetokens", namespaced: false,
		},
		{
			group: apiGroupOSOAuth, version: "v1", kind: "OAuthClientAuthorization",
			listKind: "OAuthClientAuthorizationList", plural: "oauthclientauthorizations", namespaced: false,
		},
	}
}

// openshiftSecurityDefs registers security.openshift.io/v1 SecurityContextConstraints
// (the pod-security policy predating PSA) and RangeAllocation. Both cluster-scoped.
func openshiftSecurityDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupOSSecurity, version: "v1", kind: "SecurityContextConstraints",
			listKind: "SecurityContextConstraintsList", plural: "securitycontextconstraints", namespaced: false,
		},
		{
			group: apiGroupOSSecurity, version: "v1", kind: "RangeAllocation", listKind: "RangeAllocationList",
			plural: "rangeallocations", namespaced: false,
		},
	}
}

// openshiftQuotaDefs registers quota.openshift.io/v1 ClusterResourceQuota — a
// multi-namespace quota, cluster-scoped with a status subresource.
// (AppliedClusterResourceQuota is a per-namespace derived view, not a store.)
func openshiftQuotaDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupOSQuota, version: "v1", kind: "ClusterResourceQuota",
			listKind: "ClusterResourceQuotaList", plural: "clusterresourcequotas", namespaced: false, hasStatus: true,
		},
	}
}

// openshiftAuthorizationDefs registers the legacy authorization.openshift.io/v1
// Role/RoleBinding/ClusterRole/ClusterRoleBinding views (OpenShift's RBAC
// predating rbac.authorization.k8s.io) plus RoleBindingRestriction. The
// *Review verbs are POST-only and handled separately.
func openshiftAuthorizationDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupOSAuthorization, version: "v1", kind: "Role", listKind: "RoleList",
			plural: "roles", namespaced: true,
		},
		{
			group: apiGroupOSAuthorization, version: "v1", kind: "RoleBinding", listKind: "RoleBindingList",
			plural: "rolebindings", namespaced: true,
		},
		{
			group: apiGroupOSAuthorization, version: "v1", kind: "ClusterRole", listKind: "ClusterRoleList",
			plural: "clusterroles", namespaced: false,
		},
		{
			group: apiGroupOSAuthorization, version: "v1", kind: "ClusterRoleBinding",
			listKind: "ClusterRoleBindingList", plural: "clusterrolebindings", namespaced: false,
		},
		{
			group: apiGroupOSAuthorization, version: "v1", kind: "RoleBindingRestriction",
			listKind: "RoleBindingRestrictionList", plural: "rolebindingrestrictions", namespaced: true,
		},
	}
}

// openshiftTemplateDefs registers template.openshift.io/v1 Template (namespaced),
// TemplateInstance (namespaced, status), and BrokerTemplateInstance (cluster).
func openshiftTemplateDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupOSTemplate, version: "v1", kind: "Template", listKind: "TemplateList",
			plural: "templates", namespaced: true,
		},
		{
			group: apiGroupOSTemplate, version: "v1", kind: "TemplateInstance", listKind: "TemplateInstanceList",
			plural: "templateinstances", namespaced: true, hasStatus: true,
		},
		{
			group: apiGroupOSTemplate, version: "v1", kind: "BrokerTemplateInstance",
			listKind: "BrokerTemplateInstanceList", plural: "brokertemplateinstances", namespaced: false,
		},
	}
}

// openshiftConsoleDefs registers the console.openshift.io/v1 web-console
// customization kinds (all cluster-scoped) — the objects `oc get consolelink`
// / the console operator manage.
func openshiftConsoleDefs() []*resourceDef {
	kinds := []struct{ kind, plural string }{
		{"ConsoleCLIDownload", "consoleclidownloads"},
		{"ConsoleExternalLogLink", "consoleexternalloglinks"},
		{"ConsoleLink", "consolelinks"},
		{"ConsoleNotification", "consolenotifications"},
		{"ConsolePlugin", "consoleplugins"},
		{"ConsoleQuickStart", "consolequickstarts"},
		{"ConsoleSample", "consolesamples"},
		{"ConsoleYAMLSample", "consoleyamlsamples"},
	}

	defs := make([]*resourceDef, 0, len(kinds))
	for _, k := range kinds {
		defs = append(defs, &resourceDef{
			group: apiGroupOSConsole, version: "v1", kind: k.kind, listKind: k.kind + "List",
			plural: k.plural, namespaced: false,
		})
	}

	return defs
}

// openshiftOperatorDefs registers operator.openshift.io/v1 kinds: the
// cluster-scoped second-level operator config singletons plus the namespaced
// IngressController (the object that manages routers/Routes).
func openshiftOperatorDefs() []*resourceDef {
	singletons := []struct{ kind, plural string }{
		{"Authentication", "authentications"},
		{"CloudCredential", "cloudcredentials"},
		{"Config", "configs"},
		{"Console", "consoles"},
		{"CSISnapshotController", "csisnapshotcontrollers"},
		{"DNS", "dnses"},
		{"Etcd", "etcds"},
		{"KubeAPIServer", "kubeapiservers"},
		{"KubeControllerManager", "kubecontrollermanagers"},
		{"KubeScheduler", "kubeschedulers"},
		{"Network", "networks"},
		{"OpenShiftAPIServer", "openshiftapiservers"},
		{"OpenShiftControllerManager", "openshiftcontrollermanagers"},
		{"ServiceCA", "servicecas"},
		{"Storage", "storages"},
	}

	defs := make([]*resourceDef, 0, len(singletons)+1)
	for _, k := range singletons {
		defs = append(defs, &resourceDef{
			group: apiGroupOSOperator, version: "v1", kind: k.kind, listKind: k.kind + "List",
			plural: k.plural, namespaced: false, hasStatus: true,
		})
	}

	return append(defs, &resourceDef{
		group: apiGroupOSOperator, version: "v1", kind: "IngressController", listKind: "IngressControllerList",
		plural: "ingresscontrollers", namespaced: true, hasStatus: true,
	})
}

// openshiftMachineDefs registers machine.openshift.io Machine/MachineSet/
// MachineHealthCheck (v1beta1, namespaced) and ControlPlaneMachineSet (v1).
func openshiftMachineDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupOSMachine, version: "v1beta1", kind: "Machine", listKind: "MachineList",
			plural: "machines", namespaced: true, hasStatus: true,
		},
		{
			group: apiGroupOSMachine, version: "v1beta1", kind: "MachineSet", listKind: "MachineSetList",
			plural: "machinesets", namespaced: true, hasStatus: true, hasScale: true,
		},
		{
			group: apiGroupOSMachine, version: "v1beta1", kind: "MachineHealthCheck",
			listKind: "MachineHealthCheckList", plural: "machinehealthchecks", namespaced: true, hasStatus: true,
		},
		{
			group: apiGroupOSMachine, version: "v1", kind: "ControlPlaneMachineSet",
			listKind: "ControlPlaneMachineSetList", plural: "controlplanemachinesets",
			namespaced: true, hasStatus: true, hasScale: true,
		},
	}
}

// openshiftAutoscalingDefs registers autoscaling.openshift.io ClusterAutoscaler
// (v1, cluster) and MachineAutoscaler (v1beta1, namespaced).
func openshiftAutoscalingDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupOSAutoscaling, version: "v1", kind: "ClusterAutoscaler",
			listKind: "ClusterAutoscalerList", plural: "clusterautoscalers", namespaced: false, hasStatus: true,
		},
		{
			group: apiGroupOSAutoscaling, version: "v1beta1", kind: "MachineAutoscaler",
			listKind: "MachineAutoscalerList", plural: "machineautoscalers", namespaced: true, hasStatus: true,
		},
	}
}
