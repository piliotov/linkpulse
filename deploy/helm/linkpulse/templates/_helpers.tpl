{{- define "linkpulse.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "linkpulse.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
app.kubernetes.io/part-of: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end -}}

{{- define "linkpulse.selectorLabels" -}}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/name: {{ .name }}
{{- end -}}

{{- define "linkpulse.redisAddr" -}}
{{- if .Values.redis.enabled -}}
{{ include "linkpulse.fullname" . }}-redis:6379
{{- else -}}
{{ required "redis.externalAddr is required when redis.enabled is false" .Values.redis.externalAddr }}
{{- end -}}
{{- end -}}

{{- define "linkpulse.image" -}}
{{- $tag := .svc.image.tag | default .root.Chart.AppVersion -}}
{{- if .root.Values.imageRegistry -}}
{{ printf "%s/%s:%s" .root.Values.imageRegistry .svc.image.repository $tag }}
{{- else -}}
{{ printf "%s:%s" .svc.image.repository $tag }}
{{- end -}}
{{- end -}}
