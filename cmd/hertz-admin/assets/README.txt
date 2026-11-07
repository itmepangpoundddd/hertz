This directory is embedded into HERTZ.exe.

scripts/build-admin.ps1 builds the HERTZ engine and places it here as
hertz-engine.exe before compiling the admin application. Keeping this README
here means `go build ./...` works even before the engine asset has been built.
