package sources

// These agents keep sessions as rows in a database they own. A deleted row
// cannot be put back without writing into that database, under its schema
// and its own ids for every message and part, which the index does not hold.
func init() {
	refuseWriteBack("opencode", "opencode keeps sessions as rows in its own database (opencode.db), and the index has none of the message and part ids those rows need; deja writes back only transcript files")
	refuseWriteBack("kilocode", "Kilo keeps sessions as rows in its own database (kilo.db), and the index has none of the message and part ids those rows need; deja writes back only transcript files")
	refuseWriteBack("goose", "goose keeps sessions as rows in its sessions.db and reads no transcript file back in; deja writes back only transcript files")
	refuseWriteBack("hermes", "Hermes keeps sessions as rows in its state.db (or Postgres); deja writes back only transcript files")
	refuseWriteBack("crush", "Crush keeps sessions as rows in its crush.db; deja writes back only transcript files")
}
