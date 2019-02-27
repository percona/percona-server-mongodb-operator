package templates

var (
	ConnectedNodes = `          Node ID                            Cluster ID                   Node Type                   Node Name 
------------------------------------   ------------------------   --------------------------   ------------------------------		
{{range .}}
{{- .Id | printf "%-36s"}} - {{.ClusterId | printf "%-24s"}} - {{.NodeType | printf "%-26s"}} - {{.NodeName | printf "%-30s"}}
{{end}}
	`
	ConnectedNodesVerbose = `          Node ID                            Cluster ID                   Node Type                   Node Name                     Replicaset ID
------------------------------------   ------------------------   --------------------------   ------------------------------   ------------------------------------
{{range .}}
{{- .Id | printf "%-36s"}} - {{.ClusterId | printf "%-24s"}} - {{.NodeType | printf "%-26s"}} - {{.NodeName | printf "%-30s"}} - {{.ReplicasetId | printf "%-36s"}}
{{end}}`

	AvailableBackups = `        Metadata file name     -         Description
------------------------------ - ---------------------------------------------------------------------------
{{range $name, $backup := .}}
{{- $name | printf "%-30s"}} - {{$backup.Description}}
{{end}}`

	AvailableStorages = `Available Storages:
-------------------------------------------------------------------------------------------------
{{- range .}}
Name         : {{.Name}}
MatchClients : {{range .MatchClients}}{{.}},{{end}}
DifferClients: {{range .DifferClients}}{{.}},{{end}}
{{- if or .DifferClients (not .Info.Valid) }}Storage configuration is invalid. 
  {{- if .DifferClients }} Not all clients have the same storages{{end}}
  {{- if not .Info.Valid }}
    {{- if eq .Info.Type "filesystem" }} Check filesystem path{{end}}
	{{- if eq .Info.Type "s3"}} Check S3 configuration (region, bucket name and credentials){{end}}
  {{end}}
{{- else}}
Storage configuration is valid.
{{- end}}
Type: {{.Info.Type}}
{{- if eq .Info.Type "s3" }}
  Region      : {{.Info.S3.Region}}
  Endpoint URI: {{.Info.S3.EndpointUrl}}
  Bucket      : {{.Info.S3.Bucket}}
{{- end}}
{{- if eq .Info.Type "filesystem"}}
  Path       : {{.Info.Filesystem.Path}}
{{- end}}
-------------------------------------------------------------------------------------------------
{{- end}}
`
)
