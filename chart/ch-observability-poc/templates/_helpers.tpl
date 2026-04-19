{{- define "ch-observability-poc.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "ch-observability-poc.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "ch-observability-poc.grafanaServiceAccountName" -}}
{{- $name := "" -}}
{{- if and (hasKey .Values "grafana") (hasKey .Values.grafana "serviceAccount") (hasKey .Values.grafana.serviceAccount "name") -}}
{{- $name = .Values.grafana.serviceAccount.name -}}
{{- end -}}
{{- if ne $name "" -}}
{{- $name -}}
{{- else if and (hasKey .Values "grafana") (hasKey .Values.grafana "serviceAccount") (hasKey .Values.grafana.serviceAccount "create") .Values.grafana.serviceAccount.create -}}
{{- printf "%s-grafana" (include "ch-observability-poc.fullname" .) -}}
{{- else -}}
{{- "" -}}
{{- end -}}
{{- end -}}

{{- define "ch-observability-poc.grafanaNativePlugins" -}}
{{- $grafana := .Values.grafana | default dict -}}
{{- $plugins := dig "plugins" list $grafana -}}
{{- if and (kindIs "slice" $plugins) (gt (len $plugins) 0) -}}
{{- range $plugin := $plugins }}
{{- $name := trim (dig "name" "" $plugin) -}}
{{- if ne $name "" }}
- name: {{ $name | quote }}
  version: {{ default "latest" (dig "version" "latest" $plugin) | quote }}
{{- end }}
{{- end }}
{{- end -}}
{{- end -}}

{{- define "ch-observability-poc.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | quote }}
app.kubernetes.io/name: {{ include "ch-observability-poc.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "ch-observability-poc.selectorLabels" -}}
app.kubernetes.io/name: {{ include "ch-observability-poc.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "ch-observability-poc.componentLabels" -}}
{{ include "ch-observability-poc.labels" . }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/* Render a value using Helm's tpl with a provided context.
Accepts:
- value: scalar/map/slice/string
- context: render context
*/}}
{{- define "ch-observability-poc.renderValue" -}}
{{- $value := .value | default "" -}}
{{- $context := .context -}}
{{- if kindIs "string" $value -}}
{{- tpl $value $context -}}
{{- else if or (kindIs "map" $value) (kindIs "slice" $value) (kindIs "invalid" $value) -}}
{{- tpl (toYaml $value) $context -}}
{{- else -}}
{{- tpl (printf "%v" $value) $context -}}
{{- end -}}
{{- end -}}

{{/* Render YAML from templatable value. */}}
{{- define "ch-observability-poc.renderYaml" -}}
{{- include "ch-observability-poc.renderValue" (dict "value" .value "context" .context) -}}
{{- end -}}

{{/*
Render an image reference from repository + optional tag.
If tag is empty, returns repository only.
*/}}
{{- define "ch-observability-poc.imageRef" -}}
{{- $repository := .repository | default "" | trim -}}
{{- $tag := .tag | default "" | trim -}}
{{- if eq $repository "" -}}
{{- fail "ch-observability-poc.imageRef: image repository is required" -}}
{{- end -}}
{{- if ne $tag "" -}}
{{- printf "%s:%s" $repository $tag -}}
{{- else -}}
{{- printf "%s" $repository -}}
{{- end -}}
{{- end -}}

{{/*
Normalize component resources into canonical map with requests/limits.
Supports two shapes:
1) Explicit Kubernetes style:
   resources:
     requests:
       cpu: ...
       memory: ...
     limits:
       cpu: ...
       memory: ...

2) Simplified shape:
   resources:
     cpu:
       min: 100m
       max: 500m
     memory:
       min: 256Mi
       max: 1Gi

The helper prefers explicit shape when present.
*/}}
{{- define "ch-observability-poc.normalizedResources" -}}
{{- $context := .context | default dict -}}
{{- $resources := dig "resources" dict $context -}}
{{- $result := dict -}}

{{- $explicitRequests := dig "requests" dict $resources -}}
{{- $explicitLimits := dig "limits" dict $resources -}}

{{- if and (kindIs "map" $explicitRequests) (gt (len $explicitRequests) 0) -}}
{{- $_ := set $result "requests" $explicitRequests -}}
{{- end -}}

{{- if and (kindIs "map" $explicitLimits) (gt (len $explicitLimits) 0) -}}
{{- $_ := set $result "limits" $explicitLimits -}}
{{- end -}}

{{- if and (not (hasKey $result "requests")) (not (hasKey $result "limits")) -}}
{{- $requests := dict -}}
{{- $limits := dict -}}

{{- $cpuMin := default "" (dig "cpu" "min" "" $resources) -}}
{{- if and (eq $cpuMin "") (kindIs "string" (dig "cpu" "" "" $resources)) -}}
{{- $cpuMin = dig "cpu" "" "" $resources -}}
{{- end -}}
{{- $cpuMax := default "" (dig "cpu" "limit" "" $resources) -}}
{{- if eq $cpuMax "" -}}
{{- $cpuMax = default "" (dig "cpu" "max" "" $resources) -}}
{{- end -}}

{{- $memoryMin := default "" (dig "memory" "min" "" $resources) -}}
{{- if and (eq $memoryMin "") (kindIs "string" (dig "memory" "" "" $resources)) -}}
{{- $memoryMin = dig "memory" "" "" $resources -}}
{{- end -}}
{{- $memoryMax := default "" (dig "memory" "limit" "" $resources) -}}
{{- if eq $memoryMax "" -}}
{{- $memoryMax = default "" (dig "memory" "max" "" $resources) -}}
{{- end -}}

{{- if ne $cpuMin "" -}}
{{- $_ := set $requests "cpu" $cpuMin -}}
{{- end -}}
{{- if ne $memoryMin "" -}}
{{- $_ := set $requests "memory" $memoryMin -}}
{{- end -}}
{{- if ne $cpuMax "" -}}
{{- $_ := set $limits "cpu" $cpuMax -}}
{{- end -}}
{{- if ne $memoryMax "" -}}
{{- $_ := set $limits "memory" $memoryMax -}}
{{- end -}}

{{- if gt (len $requests) 0 -}}
{{- $_ := set $result "requests" $requests -}}
{{- end -}}
{{- if gt (len $limits) 0 -}}
{{- $_ := set $result "limits" $limits -}}
{{- end -}}
{{- end -}}

{{- toYaml $result -}}
{{- end -}}

{{/*
Normalize VPA settings. Returns {enabled, updateMode, minAllowed, maxAllowed}.
When min/max constraints are omitted, defaults derive from normalized request/limit values.
*/}}
{{- define "ch-observability-poc.normalizedVPA" -}}
{{- $context := .context | default dict -}}
{{- $vpa := dig "vpa" dict $context -}}
{{- if not (dig "enabled" false $vpa) -}}
{{- dict | toYaml -}}
{{- else -}}
{{- $updateMode := default "Auto" (dig "updateMode" "Auto" $vpa) -}}
{{- $resources := include "ch-observability-poc.normalizedResources" (dict "context" $context) | fromYaml -}}
{{- $result := dict "enabled" true "updateMode" $updateMode -}}

{{- $minAllowed := dict -}}
{{- $maxAllowed := dict -}}

{{- $vpaMin := dig "minAllowed" dict $vpa -}}
{{- $vpaMax := dig "maxAllowed" dict $vpa -}}

{{- $minCpu := default "" (dig "cpu" "" $vpaMin) -}}
{{- if eq $minCpu "" -}}
{{- $minCpu = default "" (dig "requests" "cpu" "" $resources) -}}
{{- end -}}
{{- if ne $minCpu "" -}}
{{- $_ := set $minAllowed "cpu" $minCpu -}}
{{- end -}}

{{- $minMemory := default "" (dig "memory" "" $vpaMin) -}}
{{- if eq $minMemory "" -}}
{{- $minMemory = default "" (dig "requests" "memory" "" $resources) -}}
{{- end -}}
{{- if ne $minMemory "" -}}
{{- $_ := set $minAllowed "memory" $minMemory -}}
{{- end -}}

{{- $maxCpu := default "" (dig "cpu" "" $vpaMax) -}}
{{- if eq $maxCpu "" -}}
{{- $maxCpu = default "" (dig "limits" "cpu" "" $resources) -}}
{{- end -}}
{{- if ne $maxCpu "" -}}
{{- $_ := set $maxAllowed "cpu" $maxCpu -}}
{{- end -}}

{{- $maxMemory := default "" (dig "memory" "" $vpaMax) -}}
{{- if eq $maxMemory "" -}}
{{- $maxMemory = default "" (dig "limits" "memory" "" $resources) -}}
{{- end -}}
{{- if ne $maxMemory "" -}}
{{- $_ := set $maxAllowed "memory" $maxMemory -}}
{{- end -}}

{{- if gt (len $minAllowed) 0 -}}
{{- $_ := set $result "minAllowed" $minAllowed -}}
{{- end -}}
{{- if gt (len $maxAllowed) 0 -}}
{{- $_ := set $result "maxAllowed" $maxAllowed -}}
{{- end -}}

{{- toYaml $result -}}
{{- end -}}
{{- end -}}

{{/*
VPA base manifest.
Expected keys in dict:
- global: root context
- name: resource name
- targetApiVersion: e.g. apps/v1
- targetKind: e.g. Deployment
- targetName: workload name
- updateMode: Auto|Recreate|Initial|Off
- minAllowed: optional map
- maxAllowed: optional map
*/}}
{{- define "ch-observability-poc.vpa.base" -}}
apiVersion: autoscaling.k8s.io/v1
kind: VerticalPodAutoscaler
metadata:
  name: {{ .name }}
  labels:
    {{- include "ch-observability-poc.labels" .global | nindent 4 }}
    app.kubernetes.io/component: vpa
spec:
  targetRef:
    apiVersion: {{ .targetApiVersion }}
    kind: {{ .targetKind }}
    name: {{ .targetName }}
  updatePolicy:
    updateMode: {{ .updateMode | quote }}
{{- if or (and .minAllowed (gt (len .minAllowed) 0)) (and .maxAllowed (gt (len .maxAllowed) 0)) -}}
  resourcePolicy:
    containerPolicies:
      - containerName: "*"
{{- if and .minAllowed (gt (len .minAllowed) 0) -}}
        minAllowed:
          {{- toYaml .minAllowed | nindent 10 }}
{{- end }}
{{- if and .maxAllowed (gt (len .maxAllowed) 0) -}}
        maxAllowed:
          {{- toYaml .maxAllowed | nindent 10 }}
{{- end }}
{{- end -}}
{{- end -}}

{{/*
Base HTTPRoute manifest used by expose entries.
*/}}
{{- define "ch-observability-poc.httpRoute.base" -}}
{{- $serviceName := .serviceName -}}
{{- $routeName := .routeName -}}
{{- $exp := .expose -}}
{{- $backendPort := .backendPort -}}
{{- $global := .global -}}
{{- $gatewayNamespace := .gatewayNamespace | default "envoy-gateway-system" -}}
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{ $routeName }}
  labels:
    {{- include "ch-observability-poc.labels" $global | nindent 4 }}
    app.kubernetes.io/component: expose
spec:
  hostnames:
  {{- range $host := $exp.hosts }}
    - {{ tpl $host $global | quote }}
  {{- end }}
  parentRefs:
  {{- range $scope := $exp.scopes }}
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: {{ $scope }}
      namespace: {{ $gatewayNamespace | quote }}
  {{- end }}
  rules:
    - matches:
{{- if $exp.paths }}
      {{- range $path := $exp.paths }}
      - path:
          type: PathPrefix
          value: {{ $path | quote }}
      {{- end }}
{{- else }}
      - path:
          type: PathPrefix
          value: /
{{- end }}
      backendRefs:
        - group: ""
          kind: Service
          name: {{ $serviceName | quote }}
          port: {{ $backendPort }}
          weight: 1
{{- end -}}

{{/*
Generic merge engine based on component/resource patch declarations.

Usage pattern:
- Define a base manifest as a YAML string (possibly multi-document with --- separators).
- Add optional patches under .Values.merge (global) and .Values.<component>.merge (local).

Patch format:
- kind: "Kind"
- name: optional metadata.name selector
- ...any fields... merge rules:
  - scalar/objects: overwrite
  - maps: recursive merge
  - lists with {$append: [...]}: append items
*/}}
{{- define "ch-observability-poc.applyMerge" -}}
{{- $baseText := trim (required "ch-observability-poc.applyMerge: base cannot be empty" .base) -}}
{{- $global := .global -}}
{{- $context := .context | default dict -}}
{{- $docsText := splitList "\n---\n" $baseText -}}
{{- $docs := list -}}
{{- range $docText := $docsText -}}
  {{- $docText = trim $docText -}}
  {{- if ne $docText "" -}}
    {{- $doc := fromYaml $docText -}}
    {{- if kindIs "map" $doc -}}
      {{- $merged := include "ch-observability-poc.merge" (dict "base" $doc "global" $global "context" $context) | fromYaml -}}
      {{- $docs = append $docs $merged -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- range $i, $doc := $docs -}}
{{- if gt $i 0 }}
---
{{- end }}
{{ toYaml $doc }}
{{- end -}}
{{- end -}}

{{- define "ch-observability-poc.merge" -}}
{{- $base := .base -}}
{{- $global := .global -}}
{{- $context := .context | default dict -}}
{{- $merged := mustDeepCopy $base -}}
{{- $resourceKind := get $merged "kind" | default "" -}}
{{- $resourceMeta := get $merged "metadata" | default dict -}}
{{- $resourceName := get $resourceMeta "name" | default "" -}}

{{- range $patch := $global.Values.merge | default list -}}
  {{- $patchKind := $patch.kind | default "" -}}
  {{- $patchName := $patch.name | default "" -}}
  {{- if and (eq $patchKind $resourceKind) (or (eq $patchName "") (eq $patchName $resourceName)) -}}
    {{- $rawPatch := omit $patch "kind" "name" -}}
    {{- $patchData := $rawPatch -}}
    {{- if eq (get $patch "_template") true -}}
      {{- $patchData = include "ch-observability-poc.renderYaml" (dict "value" $rawPatch "context" $global) | fromYaml -}}
    {{- end -}}
    {{- $merged = include "ch-observability-poc.deepMergeWithAppend" (dict "base" $merged "patch" $patchData) | fromYaml -}}
  {{- end -}}
{{- end -}}

{{- range $patch := $context.merge | default list -}}
  {{- $patchKind := $patch.kind | default "" -}}
  {{- $patchName := $patch.name | default "" -}}
  {{- if and (eq $patchKind $resourceKind) (or (eq $patchName "") (eq $patchName $resourceName)) -}}
    {{- $rawPatch := omit $patch "kind" "name" -}}
    {{- $patchData := $rawPatch -}}
    {{- if eq (get $patch "_template") true -}}
      {{- $patchData = include "ch-observability-poc.renderYaml" (dict "value" $rawPatch "context" $global) | fromYaml -}}
    {{- end -}}
    {{- $merged = include "ch-observability-poc.deepMergeWithAppend" (dict "base" $merged "patch" $patchData) | fromYaml -}}
  {{- end -}}
{{- end -}}

{{- toYaml $merged -}}
{{- end -}}

{{- define "ch-observability-poc.deepMergeWithAppend" -}}
{{- $base := .base -}}
{{- $patch := .patch -}}
{{- $result := mustDeepCopy $base -}}
{{- range $key, $patchValue := $patch -}}
  {{- $baseValue := get $result $key -}}
  {{- if and (kindIs "map" $patchValue) (hasKey $patchValue "$append") -}}
    {{- $appendItems := get $patchValue "$append" -}}
    {{- if kindIs "slice" $baseValue -}}
      {{- $newList := $baseValue -}}
      {{- range $item := $appendItems -}}
        {{- $newList = append $newList $item -}}
      {{- end -}}
      {{- $_ := set $result $key $newList -}}
    {{- else if kindIs "invalid" $baseValue -}}
      {{- $_ := set $result $key $appendItems -}}
    {{- else -}}
      {{- $_ := set $result $key $appendItems -}}
    {{- end -}}
  {{- else if and (kindIs "map" $patchValue) (kindIs "map" $baseValue) -}}
    {{- $mergedChild := include "ch-observability-poc.deepMergeWithAppend" (dict "base" $baseValue "patch" $patchValue) | fromYaml -}}
    {{- $_ := set $result $key $mergedChild -}}
  {{- else -}}
    {{- $_ := set $result $key $patchValue -}}
  {{- end -}}
{{- end -}}
{{- toYaml $result -}}
{{- end -}}
