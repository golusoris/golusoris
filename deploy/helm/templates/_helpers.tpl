{{/*
Expand the name of the chart.
*/}}
{{- define "golusoris-app.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "golusoris-app.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart label.
*/}}
{{- define "golusoris-app.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "golusoris-app.labels" -}}
helm.sh/chart: {{ include "golusoris-app.chart" . }}
{{ include "golusoris-app.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "golusoris-app.selectorLabels" -}}
app.kubernetes.io/name: {{ include "golusoris-app.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name.
*/}}
{{- define "golusoris-app.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "golusoris-app.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Validate the graceful-drain values and resolve where the drain window runs:
in the kubelet preStop sleep (drain.preStop) or in the app after SIGTERM.
*/}}
{{- define "golusoris-app.drain" -}}
{{- $drain := .Values.drain | default dict -}}
{{- $delay := toString $drain.delaySeconds -}}
{{- $shutdown := toString $drain.shutdownSeconds -}}
{{- $grace := toString .Values.terminationGracePeriodSeconds -}}
{{- $prefix := toString $drain.envPrefix -}}
{{- if not (regexMatch "^[0-9]{1,4}$" $delay) -}}
{{- fail "drain.delaySeconds must be an integer from 0 to 9999" -}}
{{- end -}}
{{- if not (regexMatch "^[1-9][0-9]{0,3}$" $shutdown) -}}
{{- fail "drain.shutdownSeconds must be an integer from 1 to 9999" -}}
{{- end -}}
{{- if not (kindIs "bool" $drain.preStop) -}}
{{- fail "drain.preStop must be a boolean" -}}
{{- end -}}
{{- if not (regexMatch "^([A-Z][A-Z0-9_]*_)?$" $prefix) -}}
{{- fail "drain.envPrefix must be empty or an uppercase prefix ending in _" -}}
{{- end -}}
{{- range $key := list "HEALTH_DRAIN_DELAY" "HTTP_TIMEOUTS_SHUTDOWN" -}}
{{- if hasKey ($.Values.env | default dict) (printf "%s%s" $prefix $key) -}}
{{- fail (printf "env.%s%s is set by drain.*; configure drain.delaySeconds or drain.shutdownSeconds instead" $prefix $key) -}}
{{- end -}}
{{- end -}}
{{- $needed := add (atoi $delay) (atoi $shutdown) -}}
{{- if or (not (regexMatch "^[0-9]{1,5}$" $grace)) (le (atoi $grace) $needed) -}}
{{- fail (printf "terminationGracePeriodSeconds must be an integer greater than drain.delaySeconds + drain.shutdownSeconds (%d)" $needed) -}}
{{- end -}}
{{- $preStop := ternary (atoi $delay) 0 $drain.preStop -}}
preStopSeconds: {{ $preStop }}
appDelaySeconds: {{ sub (atoi $delay) $preStop }}
shutdownSeconds: {{ atoi $shutdown }}
grace: {{ atoi $grace }}
envPrefix: {{ $prefix | quote }}
{{- end }}
