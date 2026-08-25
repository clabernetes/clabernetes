{{- define "managerContainerCommonEnv" -}}
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
- name: CLIENT_OPERATION_TIMEOUT_MULTIPLIER
  value: "{{ .Values.manager.clientOperationTimeoutMultiplier }}"
- name: MANAGER_LOGGER_LEVEL
  value: {{ .Values.manager.managerLogLevel }}
- name: CONTROLLER_LOGGER_LEVEL
  value: {{ .Values.manager.controllerLogLevel }}
- name: NODE_RUNTIME_IMAGE
  {{- if .Values.manager.image }}
  value: {{ .Values.manager.image }}
  {{- else if eq .Chart.Version "0.0.0" }}
  value: "ghcr.io/clabernetes/clabernetes/clabernetes-manager:dev-latest"
  {{- else }}
  value: "ghcr.io/clabernetes/clabernetes/clabernetes-manager:{{ .Chart.Version }}"
  {{- end }}
{{- end -}}
