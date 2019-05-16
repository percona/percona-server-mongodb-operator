package templates

const (
	ConnectedNodes = "" +
		"            Node ID             " +
		"            Cluster ID       " +
		"            Node Type           " +
		"            Node Name         " +
		"            Replicaset Name" +
		"           Running DB/Oplog backup\n" +
		"------------------------------------   " +
		"------------------------   " +
		"--------------------------   " +
		"------------------------------   " +
		"------------------------------   " +
		"-----------------------\n" +
		"{{range .}}" +
		`{{ .Id | printf "%-36s"}} - ` +
		`{{ .ClusterId | printf "%-24s"}} - ` +
		`{{ .NodeType | printf "%-26s"}} - ` +
		`{{ .NodeName | printf "%-30s"}} - ` +
		`{{ .ReplicasetName | printf "%-30s"}} - ` +
		`     {{ if .Status.RunningDbBackup}}Yes{{else}} No{{end}}` +
		` / {{ if .Status.RunningOplogBackup}}Yes{{else}}No {{end}}` +
		"\n" +
		`{{end}}`

	ConnectedNodesVerbose = "" +
		"`          Node ID                 " +
		"           Cluster ID        " +
		"           Node Type           " +
		"           Node Name           " +
		"           Replicaset Name" +
		"         Running DB/Oplog backup" +
		"           Replicaset ID\n" +
		"------------------------------------   " +
		"------------------------   " +
		"--------------------------   " +
		"------------------------------   " +
		"------------------------------   " +
		"----------------------- - " +
		"------------------------------------\n" +
		"{{range .}}" +
		`{{ .Id | printf "%-36s"}} - ` +
		`{{ .ClusterId | printf "%-24s"}} - ` +
		`{{ .NodeType | printf "%-26s"}} - ` +
		`{{ .NodeName | printf "%-30s"}} - ` +
		`{{ .ReplicasetName | printf "%-30s"}} - ` +
		`     {{ if .Status.RunningDbBackup}}Yes{{else}} No{{end}}` +
		` / {{ if .Status.RunningOplogBackup}}Yes{{else}}No {{end}}` +
		`          - {{ .ReplicasetId | printf "%-36s"}}` + "\n" +
		`{{end}}`

	AvailableBackups = "" +
		"        Metadata file name     -         Description\n" +
		"------------------------------ - " +
		"---------------------------------------------------------------------------\n" +
		"{{range $name, $backup := .}}" +
		`{{- $name | printf "%-30s"}} - ` +
		`{{$backup.Description}}` + "\n" +
		`{{end}}`

	AvailableStorages = "" +
		"Available Storages:\n" +
		"-------------------------------------------------------------------------------------------------\n" +
		"{{ range .}}" +
		`Name         : {{.Name}}` + "\n" +
		`MatchClients : {{range .MatchClients}}{{.}},{{end}}` + "\n" +
		`DifferClients: {{range .DifferClients}}{{.}},{{end}}` + "\n" +
		`{{- if or .DifferClients (not .Info.Valid) }}Storage configuration is invalid. ` + "\n" +
		`  {{- if .DifferClients }} Not all clients have the same storages{{end}}` + "\n" +
		`  {{- if not .Info.Valid }}` + "\n" +
		`    {{- if eq .Info.Type "filesystem" }} Check filesystem path{{end}}` + "\n" +
		`	{{- if eq .Info.Type "s3"}} Check S3 configuration (region, bucket name and credentials){{end}}` + "\n" +
		`  {{end}}` + "\n" +
		`{{- else}}` + "\n" +
		`Storage configuration is valid.` + "\n" +
		`{{- end}}` + "\n" +
		`Type: {{.Info.Type}}` + "\n" +
		`{{- if eq .Info.Type "s3" }}` + "\n" +
		`  Region      : {{.Info.S3.Region}}` + "\n" +
		`  Endpoint URI: {{.Info.S3.EndpointUrl}}` + "\n" +
		`  Bucket      : {{.Info.S3.Bucket}}` + "\n" +
		`{{- end}}` + "\n" +
		`{{- if eq .Info.Type "filesystem"}}` + "\n" +
		`  Path       : {{.Info.Filesystem.Path}}+` + "\n" +
		"{{- end}} \n" +
		"-------------------------------------------------------------------------------------------------\n" +
		"{{ end}}\n"
)
