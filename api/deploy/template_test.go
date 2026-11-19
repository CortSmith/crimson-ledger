// Package deploy holds the SAM template and its structural tests (T10).
package deploy

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ownLogStreams is the one allowed wildcard: the streams inside the function's own log group.
var ownLogStreams = regexp.MustCompile(`^\$\{(Public|Admin)FnLogs\.Arn\}:\*$`)

type doc struct {
	Rules      map[string]any            `yaml:"Rules"`
	Parameters map[string]map[string]any `yaml:"Parameters"`
	Resources  map[string]resource       `yaml:"Resources"`
}

type resource struct {
	Type       string         `yaml:"Type"`
	Properties map[string]any `yaml:"Properties"`
}

func load(t *testing.T) doc {
	t.Helper()
	raw, err := os.ReadFile("template.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var d doc
	if err := yaml.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func functions(d doc) map[string]resource {
	out := map[string]resource{}
	for n, r := range d.Resources {
		if r.Type == "AWS::Serverless::Function" {
			out[n] = r
		}
	}
	return out
}

func TestTemplate_FunctionsAreCheap(t *testing.T) {
	fns := functions(load(t))
	if len(fns) != 2 {
		t.Fatalf("want exactly 2 functions, got %d", len(fns))
	}
	for n, f := range fns {
		checkCheapFunction(t, n, f.Properties)
	}
}

func checkCheapFunction(t *testing.T, n string, p map[string]any) {
	t.Helper()
	if arch, _ := p["Architectures"].([]any); len(arch) != 1 || arch[0] != "arm64" {
		t.Errorf("%s: architecture must be arm64, got %v", n, p["Architectures"])
	}
	if mem, _ := p["MemorySize"].(int); mem < 1 || mem > 256 {
		t.Errorf("%s: MemorySize %v must be 1..256", n, p["MemorySize"])
	}
	if p["Runtime"] != "provided.al2023" || p["Tracing"] != "Disabled" {
		t.Errorf("%s: runtime %v tracing %v", n, p["Runtime"], p["Tracing"])
	}
	for key, why := range map[string]string{"VpcConfig": "NAT gateways cost money", "Policies": "use the explicit role; SAM would attach a wildcard managed policy"} {
		if _, has := p[key]; has {
			t.Errorf("%s: %s is forbidden (%s)", n, key, why)
		}
	}
	for _, key := range []string{"ReservedConcurrentExecutions", "Role"} {
		if _, has := p[key]; !has {
			t.Errorf("%s: %s is required", n, key)
		}
	}
}

func TestTemplate_NoFunctionURLs(t *testing.T) {
	// The function is reachable only through the API Gateway route, never directly.
	for n, f := range functions(load(t)) {
		if _, has := f.Properties["FunctionUrlConfig"]; has {
			t.Errorf("%s must have no Function URL (the HTTP API is the entry point)", n)
		}
	}
}

func TestTemplate_LogRetention(t *testing.T) {
	n := 0
	for name, r := range load(t).Resources {
		if r.Type != "AWS::Logs::LogGroup" {
			continue
		}
		n++
		if days, _ := r.Properties["RetentionInDays"].(int); days < 1 || days > 14 {
			t.Errorf("%s: RetentionInDays %v must be 1..14", name, r.Properties["RetentionInDays"])
		}
	}
	if n != 2 {
		t.Errorf("want 2 log groups, got %d", n)
	}
}

// policyStatements returns every Statement of every IAM role in the template.
func policyStatements(t *testing.T, d doc) map[string][]map[string]any {
	t.Helper()
	out := map[string][]map[string]any{}
	for name, r := range d.Resources {
		if r.Type != "AWS::IAM::Role" {
			continue
		}
		pols, _ := r.Properties["Policies"].([]any)
		for _, p := range pols {
			doc, _ := p.(map[string]any)["PolicyDocument"].(map[string]any)
			for _, s := range doc["Statement"].([]any) {
				out[name] = append(out[name], s.(map[string]any))
			}
		}
	}
	return out
}

func actionsOf(st map[string]any) []string {
	switch a := st["Action"].(type) {
	case string:
		return []string{a}
	case []any:
		var out []string
		for _, x := range a {
			out = append(out, x.(string))
		}
		return out
	}
	return nil
}

func TestTemplate_IAMIsLeastPrivilege(t *testing.T) {
	stmts := policyStatements(t, load(t))
	if len(stmts) != 2 {
		t.Fatalf("want 2 roles, got %d", len(stmts))
	}
	for role, list := range stmts {
		for _, st := range list {
			checkStatement(t, role, st)
		}
	}
}

func checkStatement(t *testing.T, role string, st map[string]any) {
	t.Helper()
	if st["Effect"] != "Allow" {
		t.Errorf("%s: only Allow statements expected", role)
	}
	for _, a := range actionsOf(st) {
		switch {
		case a == "*" || strings.HasSuffix(a, ":*"):
			t.Errorf("%s: wildcard action %s", role, a)
		case a == "dynamodb:Scan" || a == "dynamodb:DeleteItem":
			t.Errorf("%s: %s is forbidden (keys are disabled, never deleted; nothing may scan)", role, a)
		}
	}
	if res, ok := st["Resource"].(string); ok && strings.Contains(res, "*") && !ownLogStreams.MatchString(res) {
		t.Errorf("%s: wildcard resource %q", role, res)
	}
}

func TestTemplate_AdminOnlyTouchesKeyItems(t *testing.T) {
	for _, st := range policyStatements(t, load(t))["AdminRole"] {
		for _, a := range actionsOf(st) {
			if !strings.HasPrefix(a, "dynamodb:") {
				continue
			}
			cond, _ := st["Condition"].(map[string]any)
			if len(cond) == 0 {
				t.Errorf("admin %s must carry a dynamodb:LeadingKeys condition", a)
			}
			for _, bad := range []string{"BatchWriteItem", "TransactWriteItems", "DeleteItem", "Scan"} {
				if strings.HasSuffix(a, bad) {
					t.Errorf("admin must not have %s", a)
				}
			}
		}
	}
}

func TestTemplate_TableIsFreeTierShaped(t *testing.T) {
	tbl := load(t).Resources["Table"].Properties
	if _, has := tbl["StreamSpecification"]; has {
		t.Error("no streams")
	}
	if pitr, _ := tbl["PointInTimeRecoverySpecification"].(map[string]any); pitr["PointInTimeRecoveryEnabled"] != false {
		t.Error("PITR must be off")
	}
	if ttl, _ := tbl["TimeToLiveSpecification"].(map[string]any); ttl["AttributeName"] != "ttl" || ttl["Enabled"] != true {
		t.Error("TTL on attribute ttl required")
	}
	if gsis, _ := tbl["GlobalSecondaryIndexes"].([]any); len(gsis) != 1 {
		t.Error("exactly one GSI")
	}
}

func TestTemplate_NoUnusedParametersAndAllDescribed(t *testing.T) {
	raw, _ := os.ReadFile("template.yaml")
	for name, p := range load(t).Parameters {
		if _, ok := p["Description"]; !ok {
			t.Errorf("parameter %s needs a description", name)
		}
		if strings.Count(string(raw), "Ref "+name)+strings.Count(string(raw), "Ref: "+name)+strings.Count(string(raw), "${"+name+"}") == 0 {
			t.Errorf("parameter %s is unused", name)
		}
	}
}

func TestTemplate_OnlyExpectedResourceTypes(t *testing.T) {
	allowed := map[string]bool{
		"AWS::DynamoDB::Table": true, "AWS::Serverless::Function": true, "AWS::Logs::LogGroup": true,
		"AWS::IAM::Role": true, "AWS::Budgets::Budget": true,
		// HTTP API entry point (1 USD per million requests) and the optional custom domain
		"AWS::ApiGatewayV2::Api": true, "AWS::ApiGatewayV2::Integration": true, "AWS::ApiGatewayV2::Route": true,
		"AWS::ApiGatewayV2::Stage": true, "AWS::ApiGatewayV2::DomainName": true, "AWS::ApiGatewayV2::ApiMapping": true,
		"AWS::Lambda::Permission": true, "AWS::Route53::RecordSet": true,
	}
	for n, r := range load(t).Resources {
		if !allowed[r.Type] {
			t.Errorf("%s: resource type %s is not on the cheap allow-list (cost-lint.sh agrees)", n, r.Type)
		}
	}
}

func run(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("bash", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("bash %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestDeployScript_DryRunPrintsCommandsAndTouchesNothing(t *testing.T) {
	out := run(t, "deploy.sh", "--dry-run", "--yes", "--budget-email", "me@example.com")
	for _, want := range []string{"cloudformation deploy", "CAPABILITY_IAM", "put-public-access-block", "SecureString", "docker compose"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output lacks %q", want)
		}
	}
	if strings.Contains(out, "clk_") {
		t.Error("dry run printed a key")
	}
}

func TestDeployScript_DestroyDryRun(t *testing.T) {
	out := run(t, "deploy.sh", "--dry-run", "--yes", "--destroy")
	for _, want := range []string{"delete-stack", "delete-bucket", "delete-parameter"} {
		if !strings.Contains(out, want) {
			t.Errorf("destroy dry run lacks %q", want)
		}
	}
}

func TestDeployScript_RefusesWithoutBudgetEmail(t *testing.T) {
	if out, err := exec.Command("bash", "deploy.sh", "--dry-run", "--yes").CombinedOutput(); err == nil {
		t.Fatalf("expected failure without --budget-email, got:\n%s", out)
	}
}

func TestDeployScript_PassesShellcheckWhenInstalled(t *testing.T) {
	sc, err := exec.LookPath("shellcheck")
	if err != nil {
		t.Skip("shellcheck not installed")
	}
	for _, f := range []string{"deploy.sh", "../../scripts/build-lambda.sh", "../../scripts/cost-lint.sh"} {
		if out, err := exec.Command(sc, f).CombinedOutput(); err != nil {
			t.Errorf("shellcheck %s:\n%s", f, out)
		}
	}
}

func TestCostLint(t *testing.T) {
	run(t, "../../scripts/cost-lint.sh")
}

// ---- API Gateway HTTP API and the optional custom domain ----

func nested(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		mm, _ := cur.(map[string]any)
		cur = mm[k]
	}
	return cur
}

func TestTemplate_HttpApiIsTheCheapKindWithOneCatchAllRoute(t *testing.T) {
	d := load(t)
	if got := d.Resources["HttpApi"].Properties["ProtocolType"]; got != "HTTP" {
		t.Errorf("ProtocolType %v: HTTP API costs 1 USD per million, REST API 3.50", got)
	}
	if got := d.Resources["ApiRoute"].Properties["RouteKey"]; got != "$default" {
		t.Errorf("RouteKey %v: the Lambda does its own routing and authentication", got)
	}
	in := d.Resources["ApiIntegration"].Properties
	if in["IntegrationType"] != "AWS_PROXY" || in["PayloadFormatVersion"] != "2.0" {
		t.Errorf("integration %v: the handler reads payload format 2.0", in)
	}
	if ms, _ := in["TimeoutInMillis"].(int); ms < 10000 || ms > 29000 {
		t.Errorf("TimeoutInMillis %v must cover the function's 10 s", in["TimeoutInMillis"])
	}
	if d.Resources["ApiStage"].Properties["StageName"] != "$default" {
		t.Error("stage must be $default so URLs carry no /stage prefix")
	}
	rs := nested(d.Resources["ApiStage"].Properties, "DefaultRouteSettings").(map[string]any)
	if rs["ThrottlingRateLimit"] == nil || rs["ThrottlingBurstLimit"] == nil {
		t.Error("the stage needs a throttle (cost ceiling)")
	}
}

func TestTemplate_OnlyThisApiMayInvokeTheFunction(t *testing.T) {
	p := load(t).Resources["ApiInvokePermission"].Properties
	if p["Principal"] != "apigateway.amazonaws.com" || p["Action"] != "lambda:InvokeFunction" {
		t.Errorf("permission %v", p)
	}
	arn := fmt.Sprint(p["SourceArn"])
	if !strings.Contains(arn, "${HttpApi}") || strings.Contains(arn, ":*:") {
		t.Errorf("SourceArn %q must name this one API, not every API in the account", arn)
	}
}

func TestTemplate_CustomDomainIsOptional(t *testing.T) {
	d := load(t)
	raw, _ := os.ReadFile("template.yaml")
	for _, name := range []string{"ApiDomain", "ApiMapping", "ApiRecord"} {
		r, ok := d.Resources[name]
		if !ok {
			t.Fatalf("%s missing", name)
		}
		if !strings.Contains(string(raw), name+":\n    Type: "+r.Type+"\n    Condition: Has") && !strings.Contains(string(raw), name+":\n    Type: "+r.Type+"\n    DependsOn: ApiStage\n    Condition: Has") &&
			!strings.Contains(string(raw), name+":\n    Type: "+r.Type+"\n    Condition: HasDomain") {
			t.Errorf("%s must be conditional (a stack without DomainName creates no domain resources)", name)
		}
	}
	for _, p := range []string{"DomainName", "CertificateArn", "HostedZoneId"} {
		if prm, ok := d.Parameters[p]; !ok || prm["Default"] != "" {
			t.Errorf("parameter %s must exist with an empty default", p)
		}
	}
	if len(d.Rules) < 2 {
		t.Error("Rules must tie DomainName and CertificateArn together")
	}
	dc := d.Resources["ApiDomain"].Properties["DomainNameConfigurations"].([]any)[0].(map[string]any)
	if dc["EndpointType"] != "REGIONAL" || dc["SecurityPolicy"] != "TLS_1_2" {
		t.Errorf("domain configuration %v: regional (free certificate in this region) and TLS 1.2", dc)
	}
}

func TestTemplate_DefaultAddressOffOnlyWithACustomName(t *testing.T) {
	raw, _ := os.ReadFile("template.yaml")
	if !strings.Contains(string(raw), "DisableExecuteApiEndpoint: !If [HasDomain, true, false]") {
		t.Error("with DomainName the default execute-api address must be disabled, without it the API needs that address")
	}
}

const goodCert = "arn:aws:acm:us-east-1:123456789012:certificate/11111111-2222-3333-4444-555555555555"

func TestDeployScript_DomainDryRun(t *testing.T) {
	out := run(t, "deploy.sh", "--dry-run", "--yes", "--budget-email", "me@example.com", "--domain", "api.example.com", "--certificate-arn", goodCert)
	for _, want := range []string{"DomainName=api.example.com", "CertificateArn=" + goodCert, "CNAME record"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run lacks %q", want)
		}
	}
	zone := run(t, "deploy.sh", "--dry-run", "--yes", "--budget-email", "me@example.com", "--domain", "api.example.com", "--certificate-arn", goodCert, "--hosted-zone-id", "Z0123456789ABC")
	if !strings.Contains(zone, "HostedZoneId=Z0123456789ABC") || strings.Contains(zone, "CNAME record") {
		t.Errorf("with a hosted zone the record is created by the stack:\n%s", zone)
	}
	plain := run(t, "deploy.sh", "--dry-run", "--yes", "--budget-email", "me@example.com")
	if strings.Contains(plain, "DomainName=") {
		t.Error("without --domain the existing domain settings must be left alone")
	}
	gone := run(t, "deploy.sh", "--dry-run", "--yes", "--budget-email", "me@example.com", "--remove-domain")
	if !strings.Contains(gone, "DomainName=") || strings.Contains(gone, "CertificateArn=arn") {
		t.Error("--remove-domain must blank the domain parameters")
	}
}

func TestDeployScript_RefusesBadDomainFlags(t *testing.T) {
	base := []string{"deploy.sh", "--dry-run", "--yes", "--budget-email", "me@example.com"}
	for name, extra := range map[string][]string{
		"domain without certificate":    {"--domain", "api.example.com"},
		"certificate without domain":    {"--certificate-arn", goodCert},
		"upper case":                    {"--domain", "API.example.com", "--certificate-arn", goodCert},
		"url instead of host":           {"--domain", "https://api.example.com", "--certificate-arn", goodCert},
		"certificate in another region": {"--domain", "api.example.com", "--certificate-arn", "arn:aws:acm:eu-west-1:123456789012:certificate/abc"},
		"remove and set":                {"--remove-domain", "--domain", "api.example.com", "--certificate-arn", goodCert},
	} {
		if out, err := exec.Command("bash", append(append([]string{}, base...), extra...)...).CombinedOutput(); err == nil {
			t.Errorf("%s: expected failure, got:\n%s", name, out)
		}
	}
}
