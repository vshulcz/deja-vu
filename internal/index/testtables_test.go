package index

// testTables builds a record table covering records assembled by hand, so a
// test can round-trip through the log without standing up a manifest.
func testTables(recs ...Record) *recordTables {
	t := newRecordTables()
	for _, r := range recs {
		t.intern(r.Key)
		t.intern(r.SourcePath)
	}
	return t
}

// mustTables loads the table an index on disk was written with.
func mustTables(t interface{ Fatal(...any) }, dir string) *recordTables {
	tbl, err := loadRecordTables(dir)
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}
