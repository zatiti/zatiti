// Command zatiti is the Zatiti entrypoint: one binary that assembles the
// landed libraries into the local controller (`zatiti serve`), the generated
// product CLI over the controller's private socket, and the stdio MCP
// adapter (`zatiti mcp serve`).
//
// Startup holder: serve opens the platform, takes the exclusive installation
// lock, opens storage, runs every module's migrations, advances the
// controller generation and only then constructs the application, the
// listener and the controller. The controller receives the held ownership
// and never locks or advances again; losing the lock stops admission and
// ends the process.
//
// Transport mechanics (serve, mcp serve, help, completion) are added to the
// generated command tree and never register product operations; every
// product operation comes from the registry catalog. Credential profiles are
// startup configuration (flags or environment), never operation arguments.
package main
