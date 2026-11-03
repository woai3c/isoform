# Contacts upgrade fixture

`state.db.gz` was created by the production schema-61 storage code at
`7ca7cd10c65607ab5cd86161f115e30a6acc6d03`, using `CreatePrincipal` and
`BindAppHandle`, the same APIs as the v61 Contacts importer. It has no v62 tables;
its version stamp has never been lowered. All people and handles are synthetic.

To reproduce from the repository root (Go on PATH):

```sh
mkdir -p work/contacts-v61-generator
git archive 7ca7cd10c65607ab5cd86161f115e30a6acc6d03 wails-gui/storage wails-gui/go.mod wails-gui/go.sum | tar -x -C work/contacts-v61-generator
cp wails-gui/testdata/contacts-v61/generate.go.txt work/contacts-v61-generator/wails-gui/generate.go
cd work/contacts-v61-generator/wails-gui
go run generate.go /absolute/path/to/wails-gui/testdata/contacts-v61/state.db.gz
```

The fixture includes native-only handles, a curated owner, two native cards
sharing fields, a mixed native/vCard owner, and curated additions to an imported
person. Editor-created display-only, identifier-only and differing identifier/
display pairs are stored on that imported person for phone, email and native
identifier controls. Upgrade tests decompress it, assert schema 61 and the
absence of v62 tables before opening it with the current storage implementation, then refresh
through a fake native source.
