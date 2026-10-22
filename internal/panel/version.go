package panel

// Version is the version of bull-Tunnel: the one home of the number.
//
// It was the Python panel's package that held it; the panel is Go now, so
// the home moved here. The build script reads it from this file and stamps
// it into the binaries, and the binaries that are built without the stamp
// still say the right thing.
const Version = "1.3.0"
