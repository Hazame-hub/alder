# Conformance suite

Empty until M1.

One table-driven suite runs against both servers through the `directory.Driver`
interface and asserts identical behaviour. It is not a set of per-vendor tests
that happen to live in the same directory: a test that needs to know which
server it is talking to is a bug report about the driver.

It runs against `test/compose`, which is ready now. See that directory's README
for endpoints, credentials, and the divergences already known.

`task test:conformance` will run it. It must be green on both servers before
any feature is considered done.
