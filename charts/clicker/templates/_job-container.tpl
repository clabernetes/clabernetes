{{- define "jobContainer" -}}
- name: clicker
  {{- if .Values.image }}
  image: {{ .Values.image }}
  {{- else if eq .Chart.Version "0.0.0" }}
  image: "ghcr.io/clabernetes/clabernetes/clabernetes-manager:dev-latest"
  {{- else }}
  image: "ghcr.io/clabernetes/clabernetes/clabernetes-manager:{{ .Chart.Version }}"
  {{- end }}
  imagePullPolicy: {{ .Values.imagePullPolicy }}
  command: [
    "/clabernetes/manager",
    "clicker",
     {{- if .Values.overrideNodes }}
     "--overrideNodes",
     {{- end }}
     {{- if .Values.nodeSelector }}
     "--nodeSelector={{ .Values.nodeSelector }}",
     {{- end }}
     {{- if not .Values.cleanupConfigMap }}
     "--skipConfigMapCleanup",
     {{- end }}
     {{- if not .Values.cleanupPods }}
     "--skipPodCleanup",
     {{- end }}
  ]
  env:
    - name: APP_NAME
      value: {{ .Values.appName }}
    - name: POD_NAME
      valueFrom:
        fieldRef:
          fieldPath: metadata.name
    - name: POD_NAMESPACE
      valueFrom:
        fieldRef:
          fieldPath: metadata.namespace
    {{- if .Values.logLevel }}
    - name: CLICKER_LOGGER_LEVEL
      value: {{ .Values.logLevel }}
    {{- end }}
    {{- if .Values.workerImage }}
    - name: CLICKER_WORKER_IMAGE
      value: {{ .Values.workerImage }}
    {{- end }}
    - name: CLICKER_WORKER_COMMAND
      value: {{ .Values.command }}
    - name: CLICKER_WORKER_SCRIPT
      value: {{ .Values.script }}
    - name: CLICKER_WORKER_RESOURCES
      value: {{ .Values.workerPodResources | toYaml | quote }}
    - name: CLICKER_GLOBAL_ANNOTATIONS
      value: {{ .Values.globalAnnotations | toYaml | quote }}
    - name: CLICKER_GLOBAL_LABELS
      value: {{ .Values.globalLabels | toYaml | quote  }}
  resources:
    requests:
      memory: {{ .Values.resources.requests.memory }}
      cpu: {{ .Values.resources.requests.cpu }}
    {{- if .Values.resources.limits }}
    limits:
      memory: {{ .Values.resources.limits.memory }}
      cpu: {{ .Values.resources.limits.cpu }}
    {{- end }}
{{- end -}}
