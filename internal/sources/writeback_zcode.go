package sources

// The transcripts under ~/.zcode/projects are not what `zcode --resume` opens
// (cmd/deja/resume.go), and its database is not deja's to write (#4617).
func init() {
	refuseWriteBack("zcode", "`zcode --resume` opens only sessions in ZCode's CLI database, and deja does not write into another program's database")
}
