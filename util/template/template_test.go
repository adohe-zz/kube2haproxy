package template

import (
	"testing"

	"github.com/AdoHe/kube2haproxy/proxy"
)

func TestTemplateWriter(t *testing.T) {
	templateName := "templateName"
	templateContent := "{{ range $ip, $value := . -}}{{ $ip }} dev eth0 {{ end -}}"
	params := map[string]bool{"10.0.0.1": true, "10.0.0.2": true}
	content, _ := RenderTemplate(templateName, templateContent, params)
	if string(content) != "10.0.0.1 dev eth0 10.0.0.2 dev eth0 " {
		t.Fatalf("get unexpected content: %s", content)
	}
}

func TestTemplateHasIPFunction(t *testing.T) {
	templateName := "templateName"
	templateContent := "{{ $ips := .IPs }}{{ range $name, $service := .RouteTable}}{{ if hasIP $ips $service.ServiceInfo.ClusterIP }}{{ $service.ServiceInfo.ClusterIP }}{{ end }}{{ end }}"
	params := TemplateData{
		IPs: map[string]bool{"10.0.0.1": true},
		RouteTable: map[string]*proxy.ServiceUnit{
			"TestServiceOne": &proxy.ServiceUnit{ServiceInfo: proxy.Service{ClusterIP: "10.0.0.1"}},
			"TestServiceTwo": &proxy.ServiceUnit{ServiceInfo: proxy.Service{ClusterIP: "10.0.0.2"}},
		},
	}
	content, _ := RenderTemplateWithFuncs(templateName, templateContent, params)
	if string(content) != "10.0.0.1" {
		t.Fatalf("get unexpected content: %s", content)
	}
}
