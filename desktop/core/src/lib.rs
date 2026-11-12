//! Core of the Relay desktop client.
//!
//! The desktop shell loads the embedded Relay web interface from a loopback
//! proxy. The proxy authenticates the webview with a per-launch secret cookie,
//! injects the device token on `/api` requests (HTTP and WebSocket) and never
//! exposes that token to JavaScript or loads code from the remote server.

pub mod config;
pub mod pairing;
pub mod proxy;
pub mod secrets;

pub use config::{Config, ConfigFile};
pub use pairing::{Pairing, PairingError};
pub use proxy::{start, Running};
pub use secrets::{MemoryStore, SecretStore};
