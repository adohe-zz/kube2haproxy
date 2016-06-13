package template

import (
	"bytes"
	"text/template"

	"github.com/AdoHe/kube2haproxy/proxy"
)

type TemplateData struct {
	IPs        map[string]bool
	RouteTable map[string]*proxy.ServiceUnit
}

func hasIP(ipsMap map[string]bool, ip string) bool {
	return ipsMap[ip]
}

// Returns string content of a rendered template
func RenderTemplate(templateName, templateContent string, data interface{}) ([]byte, error) {
	tpl := template.Must(template.New(templateName).Parse(templateContent))

	strBuffer := new(bytes.Buffer)

	err := tpl.Execute(strBuffer, data)
	if err != nil {
		return nil, err
	}

	return strBuffer.Bytes(), nil
}

// Returns string content of a rendered template (with template functions)
func RenderTemplateWithFuncs(templateName, templateContent string, data interface{}) ([]byte, error) {
	funcMap := template.FuncMap{
		"hasIP": hasIP,
	}

	tpl := template.Must(template.New(templateName).Funcs(funcMap).Parse(templateContent))

	strBuffer := new(bytes.Buffer)

	err := tpl.Execute(strBuffer, data)
	if err != nil {
		return nil, err
	}

	return strBuffer.Bytes(), nil
}
