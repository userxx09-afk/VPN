{{/*
Copyright (c) Tailscale Inc & contributors
SPDX-License-Identifier: BSD-3-Clause
*/}}

{{/*
Create a default fully qualified app name, truncated at 63 chars because some
Kubernetes name fields are limited to this (by the DNS naming spec).

For backwards compatibility with existing installations, this defaults to the
historical hardcoded name "operator" unless nameOverride or fullnameOverride
is set.
*/}}
{{- define "tappervpn-operator.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else if .Values.nameOverride }}
{{- $name := .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- else }}
{{- "operator" }}
{{- end }}
{{- end }}

{{/*
Name for cluster-scoped operator resources. Defaults to the historical
hardcoded name "tappervpn-operator" unless nameOverride or fullnameOverride is
set, in which case the fullname is used instead.
*/}}
{{- define "tappervpn-operator.clusterResourceName" -}}
{{- if or .Values.fullnameOverride .Values.nameOverride }}
{{- include "tappervpn-operator.fullname" . }}
{{- else }}
{{- "tappervpn-operator" }}
{{- end }}
{{- end }}
