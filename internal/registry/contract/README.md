# contract

Recorded examples of the registry's response models, copied from
[termcade-be](https://github.com/aviorstudio/termcade-be)'s `contract/`, which
generates them from the API itself.

`catalog_test.go` decodes them into this package's types. That is the only
thing standing between the arcade's idea of the wire format and the server's:
a renamed field decodes as a zero value, and a zero value looks exactly like a
game with no release rather than like a bug.

**Copied, not shared.** A Go module cannot read files out of a sibling
repository, and vendoring the API to get four JSON files would be a dependency
on the whole backend. The cost is that these can go stale — so when the API
changes a response model it regenerates its own copy, and updating this one is
part of the change that follows here.
