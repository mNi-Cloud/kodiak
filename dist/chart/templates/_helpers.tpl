{{/*
Expand the name of the chart.
*/}}
{{- define "kodiak.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "kodiak.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "kodiak.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kodiak.labels" -}}
helm.sh/chart: {{ include "kodiak.chart" . }}
{{ include "kodiak.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kodiak.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kodiak.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Ionscale labels.
*/}}
{{- define "kodiak.ionscale.labels" -}}
{{ include "kodiak.labels" . }}
app.kubernetes.io/component: ionscale
{{- end }}

{{/*
Ionscale selector labels.
*/}}
{{- define "kodiak.ionscale.selectorLabels" -}}
{{ include "kodiak.selectorLabels" . }}
app.kubernetes.io/component: ionscale
{{- end }}

{{/*
Public Ionscale URL used by Tailscale clients.
*/}}
{{- define "kodiak.ionscale.serverURL" -}}
{{- if .Values.ionscale.serverUrl -}}
{{ .Values.ionscale.serverUrl }}
{{- else -}}
http://ionscale.{{ .Release.Namespace }}.svc.cluster.local:{{ .Values.ionscale.service.port }}
{{- end -}}
{{- end }}

{{/*
Internal Ionscale API endpoint used by the controller.
*/}}
{{- define "kodiak.ionscale.apiEndpoint" -}}
{{- if .Values.ionscale.tls.disable -}}
http://ionscale.{{ .Release.Namespace }}.svc.cluster.local:{{ .Values.ionscale.service.port }}
{{- else -}}
{{ include "kodiak.ionscale.serverURL" . }}
{{- end -}}
{{- end }}

{{/*
Manager labels
*/}}
{{- define "kodiak.manager.labels" -}}
{{ include "kodiak.labels" . }}
app.kubernetes.io/component: controller-manager
control-plane: controller-manager
{{- end }}

{{/*
Manager selector labels
*/}}
{{- define "kodiak.manager.selectorLabels" -}}
{{ include "kodiak.selectorLabels" . }}
control-plane: controller-manager
{{- end }}

{{/*
Controller manager service account name.
*/}}
{{- define "kodiak.serviceAccountName" -}}
{{- default (printf "%s-controller-manager" (include "kodiak.fullname" .)) .Values.controllerManager.serviceAccountName }}
{{- end }}
