use super::*;
const GOOD: &str = "[game]\nid='acme/blaster'\nname='Blaster'\nversion='0.1.0'\n[requirements]\nabi=1\nwidth=40\nheight=30\n[controls]\nleft='aim'\n";
const WASM: &[u8] = b"\0asm\x01\0\0\0";

// Construct raw stored ZIP records independently of the production ZIP writer.
// This preserves duplicate entries, Unix modes and encryption flags that the
// normal writer will not generate.
fn stored(entries: &[(&str, &[u8], u16, u32)]) -> Vec<u8> {
    let (mut out, mut directory) = (Vec::new(), Vec::new());
    for (name, body, flags, mode) in entries {
        let offset = out.len() as u32;
        let crc = crc32fast::hash(body);
        out.extend_from_slice(b"PK\x03\x04");
        for value in [20, *flags, 0, 0, 0] {
            out.extend_from_slice(&value.to_le_bytes());
        }
        for value in [crc, body.len() as u32, body.len() as u32] {
            out.extend_from_slice(&value.to_le_bytes());
        }
        out.extend_from_slice(&(name.len() as u16).to_le_bytes());
        out.extend_from_slice(&0_u16.to_le_bytes());
        out.extend_from_slice(name.as_bytes());
        out.extend_from_slice(body);
        directory.extend_from_slice(b"PK\x01\x02");
        for value in [0x0314_u16, 20, *flags, 0, 0, 0] {
            directory.extend_from_slice(&value.to_le_bytes());
        }
        for value in [crc, body.len() as u32, body.len() as u32] {
            directory.extend_from_slice(&value.to_le_bytes());
        }
        for value in [name.len() as u16, 0, 0, 0, 0] {
            directory.extend_from_slice(&value.to_le_bytes());
        }
        directory.extend_from_slice(&(*mode << 16).to_le_bytes());
        directory.extend_from_slice(&offset.to_le_bytes());
        directory.extend_from_slice(name.as_bytes());
    }
    let offset = out.len() as u32;
    out.extend_from_slice(&directory);
    out.extend_from_slice(b"PK\x05\x06");
    for value in [0_u16, 0, entries.len() as u16, entries.len() as u16] {
        out.extend_from_slice(&value.to_le_bytes());
    }
    out.extend_from_slice(&(directory.len() as u32).to_le_bytes());
    out.extend_from_slice(&offset.to_le_bytes());
    out.extend_from_slice(&0_u16.to_le_bytes());
    out
}

#[test]
fn manifest_identity_controls_and_host_allocation_limits_follow_the_original_contract() {
    let manifest = Manifest::parse(GOOD.as_bytes()).unwrap();
    assert_eq!((manifest.author(), manifest.slug()), ("acme", "blaster"));
    assert_eq!(
        manifest.info(),
        termcade_sdk::Info {
            id: "acme/blaster".into(),
            title: "BLASTER".into(),
            pixel_w: 40,
            pixel_h: 30
        }
    );
    assert_eq!(manifest.controls["left"], "aim");
    assert!(manifest.compatible_abi());
    for raw in [
        GOOD.replace("id='acme/blaster'", ""),
        GOOD.replace("acme/blaster", "blaster"),
        GOOD.replace("acme/blaster", "Acme/Blaster"),
        GOOD.replace("version='0.1.0'", ""),
        GOOD.replace("abi=1", ""),
        GOOD.replace("height=30", "height=31"),
        GOOD.replace("width=40", "width=100000"),
        GOOD.replace("height=30", "height=1000"),
        GOOD.replace("name='Blaster'", &format!("name='{}'", "A".repeat(33))),
        GOOD.replace("name='Blaster'", "name=\"Bl\\u001b[31master\""),
        GOOD.replace("left='aim'", "left=\"a\\u001b[2Jim\""),
        "not toml {{{".into(),
    ] {
        assert!(Manifest::parse(raw.as_bytes()).is_err(), "accepted {raw:?}");
    }
    let future = Manifest::parse(GOOD.replace("abi=1", "abi=2").as_bytes()).unwrap();
    assert!(!future.compatible_abi());
}

#[test]
fn package_rejects_extra_duplicate_nested_encrypted_nonregular_corrupt_and_oversized_entries() {
    let normal = [
        (FILE_NAME, GOOD.as_bytes(), 0, 0o100644),
        (WASM_NAME, WASM, 0, 0o100644),
    ];
    assert!(Package::read(&stored(&normal)).is_ok());
    for extra in [
        ("README.md", &b"extra"[..], 0, 0),
        (FILE_NAME, GOOD.as_bytes(), 0, 0),
        (WASM_NAME, WASM, 0, 0),
        ("sub/game.wasm", WASM, 0, 0),
        ("sub/", &b""[..], 0, 0o040755),
    ] {
        let mut entries = normal.to_vec();
        entries.push(extra);
        assert!(Package::read(&stored(&entries)).is_err());
    }
    for mode in [0o040755, 0o120777, 0o060600, 0o020600, 0o010600] {
        assert!(
            Package::read(&stored(&[(FILE_NAME, GOOD.as_bytes(), 0, mode), normal[1]])).is_err()
        );
    }
    for flags in [1, 0x801] {
        assert!(
            Package::read(&stored(&[
                (FILE_NAME, GOOD.as_bytes(), flags, 0o100644),
                normal[1]
            ]))
            .is_err()
        );
    }
    assert!(Package::read(&stored(&normal[..1])).is_err());
    assert!(
        Package::read(&stored(&[
            (FILE_NAME, GOOD.as_bytes(), 0, 0),
            (WASM_NAME, b"no wasm", 0, 0)
        ]))
        .is_err()
    );
    let mut corrupt = stored(&normal);
    let body = 30 + FILE_NAME.len();
    corrupt[body] ^= 1;
    assert!(Package::read(&corrupt).is_err());
    let mut oversized = stored(&normal);
    // First local and central entry advertise more than the manifest ceiling.
    oversized[22..26].copy_from_slice(&((MAX_MANIFEST_BYTES + 1) as u32).to_le_bytes());
    let central = oversized
        .windows(4)
        .position(|v| v == b"PK\x01\x02")
        .unwrap();
    oversized[central + 24..central + 28]
        .copy_from_slice(&((MAX_MANIFEST_BYTES + 1) as u32).to_le_bytes());
    assert!(Package::read(&oversized).is_err());
}

#[test]
fn original_bundled_go_packages_still_parse_and_install_without_changing_guest_bytes() {
    let packs = Path::new(env!("CARGO_MANIFEST_DIR")).join("../internal/starter/packs");
    let destination = tempfile::tempdir().unwrap();
    let mut names = Vec::new();
    for entry in fs::read_dir(packs).unwrap() {
        let path = entry.unwrap().path();
        if path.extension().is_none_or(|ext| ext != "tcade") {
            continue;
        }
        let package = Package::open(&path).unwrap();
        assert!(package.manifest.compatible_abi());
        let installed = package.install(destination.path()).unwrap();
        assert_eq!(fs::read(installed.join(WASM_NAME)).unwrap(), package.wasm);
        assert_eq!(
            Manifest::parse(&fs::read(installed.join(FILE_NAME)).unwrap())
                .unwrap()
                .game
                .id,
            package.manifest.game.id
        );
        assert_eq!(package.install(destination.path()).unwrap(), installed);
        names.push(package.manifest.game.id);
    }
    names.sort();
    assert_eq!(
        names,
        [
            "aviorstudio/asteroid",
            "aviorstudio/brickough",
            "aviorstudio/tetris"
        ]
    );
}

#[cfg(unix)]
#[test]
fn installation_refuses_linked_namespace_paths_and_preserves_their_targets() {
    let package = Package::read(&stored(&[
        (FILE_NAME, GOOD.as_bytes(), 0, 0),
        (WASM_NAME, WASM, 0, 0),
    ]))
    .unwrap();
    let root = tempfile::tempdir().unwrap();
    let outside = tempfile::tempdir().unwrap();
    fs::write(outside.path().join("keep"), b"keep").unwrap();
    std::os::unix::fs::symlink(outside.path(), root.path().join("acme")).unwrap();
    assert!(package.install(root.path()).is_err());
    assert_eq!(fs::read(outside.path().join("keep")).unwrap(), b"keep");
    assert!(!outside.path().join("blaster").exists());
}
