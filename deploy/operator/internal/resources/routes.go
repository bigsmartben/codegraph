package resources

import (
	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	nginxIngressClassName        = "nginx"
	nginxRewriteTargetAnnotation = "nginx.ingress.kubernetes.io/rewrite-target"
)

type RouteConfig struct {
	GatewayName      string
	GatewayNamespace string
}

func BuildHTTPRoute(repo *codegraphv1alpha1.CodeGraphRepository, config RouteConfig) *gatewayv1.HTTPRoute {
	names := NamesFor(repo)
	pathType := gatewayv1.PathMatchPathPrefix
	pathValue := repo.Spec.MCP.Path
	rewriteType := gatewayv1.PrefixMatchHTTPPathModifier
	rewritePrefix := "/mcp"
	backendPort := gatewayv1.PortNumber(MCPPort)
	parentRef := gatewayv1.ParentReference{Name: gatewayv1.ObjectName(config.GatewayName)}
	if config.GatewayNamespace != "" {
		namespace := gatewayv1.Namespace(config.GatewayNamespace)
		parentRef.Namespace = &namespace
	}

	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:            names.Route,
			Namespace:       repo.Namespace,
			Labels:          LabelsFor(repo),
			OwnerReferences: OwnerFor(repo),
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{parentRef},
			},
			Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(repo.Spec.MCP.Host)},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					Matches: []gatewayv1.HTTPRouteMatch{
						{
							Path: &gatewayv1.HTTPPathMatch{
								Type:  &pathType,
								Value: &pathValue,
							},
						},
					},
					Filters: []gatewayv1.HTTPRouteFilter{
						{
							Type: gatewayv1.HTTPRouteFilterURLRewrite,
							URLRewrite: &gatewayv1.HTTPURLRewriteFilter{
								Path: &gatewayv1.HTTPPathModifier{
									Type:               rewriteType,
									ReplacePrefixMatch: &rewritePrefix,
								},
							},
						},
					},
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: gatewayv1.ObjectName(names.Service),
									Port: &backendPort,
								},
							},
						},
					},
				},
			},
		},
	}
}

func BuildIngress(repo *codegraphv1alpha1.CodeGraphRepository) *networkingv1.Ingress {
	names := NamesFor(repo)
	pathType := networkingv1.PathTypeExact
	ingressClassName := nginxIngressClassName

	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.Route,
			Namespace: repo.Namespace,
			Labels:    LabelsFor(repo),
			Annotations: map[string]string{
				nginxRewriteTargetAnnotation: "/mcp",
			},
			OwnerReferences: OwnerFor(repo),
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &ingressClassName,
			Rules: []networkingv1.IngressRule{
				{
					Host: repo.Spec.MCP.Host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     repo.Spec.MCP.Path,
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: names.Service,
											Port: networkingv1.ServiceBackendPort{Number: MCPPort},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}
