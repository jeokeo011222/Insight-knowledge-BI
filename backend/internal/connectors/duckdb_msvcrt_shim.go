//go:build windows && cgo

package connectors

/*
#include <stdarg.h>
#include <stddef.h>
#include <stdio.h>

// DuckDB's prebuilt Windows static library calls _snprintf, which the MinGW
// runtime Go links against declares but does not provide, so the final link
// fails with "undefined reference to _snprintf".
//
// The symbol is defined through an assembler label instead of its C name: the
// MinGW headers already declare _snprintf, and a plain definition would clash
// with that declaration.
//
// MSVC's _snprintf truncates without a trailing NUL and returns -1 when the
// output does not fit; vsnprintf returns the length it would have written and
// still terminates the buffer. DuckDB only uses the result to build SQL error
// strings, where a terminated, truncated string is what callers expect.
int duckdb_snprintf_compat(char *buffer, size_t count, const char *format, ...)
	__asm__("_snprintf");

int duckdb_snprintf_compat(char *buffer, size_t count, const char *format, ...) {
	va_list args;
	va_start(args, format);
	int written = vsnprintf(buffer, count, format, args);
	va_end(args);
	return written;
}
*/
import "C"

// The shim is only needed at link time; referencing the package keeps the
// object file in the link so _snprintf stays available.
var _ = C.int(0)
