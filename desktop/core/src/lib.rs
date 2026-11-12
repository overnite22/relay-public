//! Data layer of the native Relay desktop client: a shared model, the
//! [`DataSource`] trait and its two implementations (local encrypted
//! database, paired Relay server).

pub mod local;
pub mod model;
pub mod pairing;
pub mod server;
pub mod source;
pub mod vault;

pub use local::LocalSource;
pub use model::*;
pub use server::ServerSource;
pub use source::{effective_credential, DataSource, Error, Result};
