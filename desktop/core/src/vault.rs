//! Password-based encryption for the local database secrets:
//! Argon2id key derivation and AES-256-GCM with a random nonce per value.

use aes_gcm::aead::{Aead, KeyInit, Payload};
use aes_gcm::{Aes256Gcm, Nonce};
use rand::RngCore;
use zeroize::Zeroizing;

pub const SALT_LEN: usize = 16;
const NONCE_LEN: usize = 12;

pub struct Key(Zeroizing<[u8; 32]>);

impl Key {
    pub fn derive(password: &str, salt: &[u8]) -> Key {
        let params = argon2::Params::new(64 * 1024, 3, 1, Some(32)).expect("valid Argon2 parameters");
        let argon = argon2::Argon2::new(argon2::Algorithm::Argon2id, argon2::Version::V0x13, params);
        let mut out = Zeroizing::new([0u8; 32]);
        argon
            .hash_password_into(password.as_bytes(), salt, out.as_mut())
            .expect("Argon2 output length");
        Key(out)
    }

    pub fn seal(&self, aad: &[u8], plain: &[u8]) -> Vec<u8> {
        let mut nonce = [0u8; NONCE_LEN];
        rand::rng().fill_bytes(&mut nonce);
        let cipher = Aes256Gcm::new_from_slice(self.0.as_ref()).expect("key length");
        let mut out = nonce.to_vec();
        out.extend(
            cipher
                .encrypt(Nonce::from_slice(&nonce), Payload { msg: plain, aad })
                .expect("AES-GCM encryption"),
        );
        out
    }

    pub fn open(&self, aad: &[u8], sealed: &[u8]) -> Option<Zeroizing<Vec<u8>>> {
        if sealed.len() < NONCE_LEN + 16 {
            return None;
        }
        let cipher = Aes256Gcm::new_from_slice(self.0.as_ref()).ok()?;
        let (nonce, ct) = sealed.split_at(NONCE_LEN);
        cipher
            .decrypt(Nonce::from_slice(nonce), Payload { msg: ct, aad })
            .ok()
            .map(Zeroizing::new)
    }
}

pub fn random_salt() -> [u8; SALT_LEN] {
    let mut s = [0u8; SALT_LEN];
    rand::rng().fill_bytes(&mut s);
    s
}
