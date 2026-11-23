// Package hideconsole keeps a child console program from opening a window of
// its own on Windows.
//
// When deja runs without a console (started by a GUI host, a scheduled task
// or another detached process), every console child it starts, such as
// sqlite3, git or zstd, allocates a new console, and its window flashes on
// screen for as long as the child runs. An incremental index run opened two to
// four of them (#6156). Apply gives the child a console with no window, which
// its own children then inherit. Elsewhere Apply does nothing.
package hideconsole
