{{- define "jobMetadata" -}}
labels:
  chart: "{{ .Chart.Name }}-{{ .Chart.Version }}"
  release: {{ .Release.Name }}
  heritage: {{ .Release.Service }}
  revision: "{{ .Release.Revision }}"
  c9s.run/app: {{ .Values.appName }}
  c9s.run/name: "{{ .Values.appName }}-clicker"
  c9s.run/component: clicker
  {{- $podLabels := merge .Values.globalLabels .Values.jobLabels }}
    {{- if $podLabels }}
{{ toYaml $podLabels | indent 2 }}
    {{- end }}
{{- $podAnnotations := merge .Values.globalAnnotations .Values.jobAnnotations }}
{{- if $podAnnotations }}
annotations:
{{ toYaml $podAnnotations | indent 2 }}
{{- end }}
{{- end -}}