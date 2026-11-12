use relay_desktop_core::*;
use std::collections::BTreeMap;

/// Exercises any data source with the same scenario.
fn scenario(src: &dyn DataSource) {
    let parent = src
        .save_folder(&Folder {
            name: "Serveurs Linux".into(),
            ..Default::default()
        })
        .unwrap();
    let child = src
        .save_folder(&Folder {
            name: "Prod".into(),
            parent_id: Some(parent.id.clone()),
            ..Default::default()
        })
        .unwrap();

    let cred = src
        .save_credential(
            None,
            &CredentialInput {
                name: "root lab".into(),
                username: "root".into(),
                password: Some("s3cret!".into()),
                ..Default::default()
            },
        )
        .unwrap();
    assert!(cred.has_password && !cred.has_private_key);
    assert_eq!(src.reveal(&cred.id, SecretField::Password).unwrap(), "s3cret!");

    // Updating metadata without a password keeps the stored secret.
    let renamed = src
        .save_credential(
            Some(&cred.id),
            &CredentialInput {
                name: "root".into(),
                username: "root".into(),
                ..Default::default()
            },
        )
        .unwrap();
    assert_eq!(renamed.name, "root");
    assert_eq!(src.reveal(&cred.id, SecretField::Password).unwrap(), "s3cret!");

    let mut e = Entry::new(Kind::Ssh);
    e.name = "web-01".into();
    e.host = "10.0.0.5".into();
    e.folder_id = Some(child.id.clone());
    let e = src.save_entry(&e).unwrap();
    assert!(!e.id.is_empty());
    assert_eq!(e.port, 22);

    let mut edited = e.clone();
    edited.notes = "# Notes\nok".into();
    edited.credential_id = Some(cred.id.clone());
    assert_eq!(src.save_entry(&edited).unwrap().notes, "# Notes\nok");

    let tree = src.tree().unwrap();
    assert!(tree
        .entries
        .iter()
        .any(|x| x.id == e.id && x.folder_id.as_deref() == Some(child.id.as_str())));
    assert_eq!(effective_credential(&tree, &edited), Some(cred.id.clone()));

    src.delete_entry(&e.id).unwrap();
    src.delete_credential(&cred.id).unwrap();
    src.delete_folder(&child.id).unwrap();
    src.delete_folder(&parent.id).unwrap();
    assert!(src.tree().unwrap().entries.iter().all(|x| x.id != e.id));
}

#[test]
fn local_source() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("relay.db");
    assert!(matches!(LocalSource::create(&path, "court"), Err(Error::Invalid(_))));
    let src = LocalSource::create(&path, "mot de passe maître").unwrap();
    scenario(&src);

    // Secrets are not stored in clear and need the master password.
    let c = src
        .save_credential(
            None,
            &CredentialInput {
                name: "x".into(),
                password: Some("Tr3s-Secret".into()),
                ..Default::default()
            },
        )
        .unwrap();
    drop(src);
    assert!(!std::fs::read(&path).unwrap().windows(11).any(|w| w == b"Tr3s-Secret"));
    assert!(matches!(LocalSource::open(&path, "mauvais mot de passe"), Err(Error::BadPassword)));
    let src = LocalSource::open(&path, "mot de passe maître").unwrap();
    assert_eq!(src.reveal(&c.id, SecretField::Password).unwrap(), "Tr3s-Secret");
    assert!(matches!(LocalSource::create(&path, "mot de passe maître"), Err(Error::Invalid(_))));
}

#[test]
fn local_rules() {
    let dir = tempfile::tempdir().unwrap();
    let src = LocalSource::create(&dir.path().join("r.db"), "mot de passe maître").unwrap();
    let a = src
        .save_folder(&Folder {
            name: "A".into(),
            ..Default::default()
        })
        .unwrap();
    let b = src
        .save_folder(&Folder {
            name: "B".into(),
            parent_id: Some(a.id.clone()),
            ..Default::default()
        })
        .unwrap();
    // No cycles, no deleting non-empty folders or used credentials.
    assert!(src
        .save_folder(&Folder {
            parent_id: Some(b.id.clone()),
            ..a.clone()
        })
        .is_err());
    assert!(src.delete_folder(&a.id).is_err());
    let cred = src
        .save_credential(
            None,
            &CredentialInput {
                name: "c".into(),
                ..Default::default()
            },
        )
        .unwrap();
    let mut e = Entry::new(Kind::Rdp);
    e.name = "dc".into();
    e.host = "dc.lab".into();
    e.credential_id = Some(cred.id.clone());
    src.save_entry(&e).unwrap();
    assert!(src.delete_credential(&cred.id).is_err());
    let mut ghost = Entry::new(Kind::Rdp);
    ghost.name = "x".into();
    ghost.host = "x".into();
    ghost.folder_id = Some("missing".into());
    assert!(src.save_entry(&ghost).is_err());
}

#[test]
fn inheritance_uses_nearest_folder() {
    let mut creds = BTreeMap::new();
    creds.insert("rdp".to_string(), Some("domain-admin".to_string()));
    let tree = Tree {
        folders: vec![
            Folder {
                id: "root".into(),
                name: "Root".into(),
                credentials: creds,
                ..Default::default()
            },
            Folder {
                id: "sub".into(),
                parent_id: Some("root".into()),
                name: "Sub".into(),
                ..Default::default()
            },
        ],
        entries: vec![],
    };
    let mut e = Entry::new(Kind::Rdp);
    e.folder_id = Some("sub".into());
    assert_eq!(effective_credential(&tree, &e).as_deref(), Some("domain-admin"));
    e.kind = Kind::Ssh;
    assert_eq!(effective_credential(&tree, &e), None);
}

/// Runs against a live Relay: RELAY_TEST_SERVER=http://127.0.0.1:port RELAY_TEST_TOKEN=rlt_…
#[test]
#[ignore]
fn server_source() {
    let server = std::env::var("RELAY_TEST_SERVER").unwrap();
    let token = std::env::var("RELAY_TEST_TOKEN").unwrap();
    assert!(matches!(
        ServerSource::connect(&server, "rlt_invalid", "test"),
        Err(Error::Unauthorized)
    ));
    let src = ServerSource::connect(&server, &token, "relay-desktop-test").unwrap();
    assert!(!src.user().is_empty());
    scenario(&src);
}
