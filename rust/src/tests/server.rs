// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

//! A scripted HTTP/1.1 test server, plain or TLS (optionally requiring a
//! client certificate), and throwaway certificates.

use std::collections::VecDeque;
use std::net::SocketAddr;
use std::sync::{Arc, Mutex};

use rcgen::{BasicConstraints, CertificateParams, ExtendedKeyUsagePurpose, IsCa, Issuer, KeyPair};
use rustls::pki_types::{CertificateDer, PrivateKeyDer, PrivatePkcs8KeyDer};
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};
use tokio::net::TcpListener;

#[derive(Clone, Default)]
pub(crate) struct Reply {
    pub(crate) status: u16,
    pub(crate) headers: Vec<(&'static str, &'static str)>,
    pub(crate) body: &'static str,
    pub(crate) hang: bool,
}

pub(crate) fn reply(status: u16, body: &'static str) -> Reply {
    Reply { status, body, ..Default::default() }
}

pub(crate) fn hang() -> Reply {
    Reply { hang: true, ..Default::default() }
}

#[derive(Clone, Debug)]
pub(crate) struct Recorded {
    pub(crate) request_line: String,
    pub(crate) headers: Vec<(String, String)>,
    pub(crate) body: String,
}

impl Recorded {
    pub(crate) fn header(&self, name: &str) -> Option<&str> {
        self.headers.iter().find(|(k, _)| k.eq_ignore_ascii_case(name)).map(|(_, v)| v.as_str())
    }
}

pub(crate) struct TestServer {
    pub(crate) addr: SocketAddr,
    pub(crate) requests: Arc<Mutex<Vec<Recorded>>>,
    pub(crate) ca_pem: String,
    pub(crate) pki: Pki,
}

impl TestServer {
    pub(crate) fn url(&self, scheme: &str) -> String {
        format!("{scheme}://{}", self.addr)
    }

    pub(crate) fn count(&self) -> usize {
        self.requests.lock().unwrap().len()
    }

    pub(crate) fn request(&self, i: usize) -> Recorded {
        self.requests.lock().unwrap()[i].clone()
    }
}

/// Throwaway PKI: a server CA and server certificate for 127.0.0.1, and a
/// client CA and client certificate.
pub(crate) struct Pki {
    pub(crate) server_ca_pem: String,
    server_chain: Vec<CertificateDer<'static>>,
    server_key: Vec<u8>,
    pub(crate) client_ca_der: CertificateDer<'static>,
    pub(crate) client_cert_pem: String,
    pub(crate) client_key_pem: String,
}

fn ca(name: &str) -> (CertificateParams, KeyPair, String, CertificateDer<'static>) {
    let mut params = CertificateParams::new(Vec::<String>::new()).unwrap();
    params.is_ca = IsCa::Ca(BasicConstraints::Unconstrained);
    params.distinguished_name.push(rcgen::DnType::CommonName, name);
    let key = KeyPair::generate().unwrap();
    let cert = params.self_signed(&key).unwrap();
    (params, key, cert.pem(), cert.der().clone())
}

pub(crate) fn pki() -> Pki {
    let (sca_params, sca_key, server_ca_pem, _) = ca("test server CA");
    let server_issuer = Issuer::new(sca_params, sca_key);
    let mut sp = CertificateParams::new(vec!["127.0.0.1".to_owned(), "localhost".to_owned()]).unwrap();
    sp.extended_key_usages = vec![ExtendedKeyUsagePurpose::ServerAuth];
    let skey = KeyPair::generate().unwrap();
    let scert = sp.signed_by(&skey, &server_issuer).unwrap();

    let (cca_params, cca_key, _, client_ca_der) = ca("test client CA");
    let client_issuer = Issuer::new(cca_params, cca_key);
    let mut cp = CertificateParams::new(Vec::<String>::new()).unwrap();
    cp.distinguished_name.push(rcgen::DnType::CommonName, "client");
    cp.extended_key_usages = vec![ExtendedKeyUsagePurpose::ClientAuth];
    let ckey = KeyPair::generate().unwrap();
    let ccert = cp.signed_by(&ckey, &client_issuer).unwrap();

    Pki {
        server_ca_pem,
        server_chain: vec![scert.der().clone()],
        server_key: skey.serialize_der(),
        client_ca_der,
        client_cert_pem: ccert.pem(),
        client_key_pem: ckey.serialize_pem(),
    }
}

pub(crate) enum Mode {
    Plain,
    Tls,
    MutualTls,
}

/// Starts a server that answers requests with `replies` in order.
pub(crate) async fn start(mode: Mode, replies: Vec<Reply>) -> TestServer {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let requests = Arc::new(Mutex::new(Vec::new()));
    let replies = Arc::new(Mutex::new(VecDeque::from(replies)));
    let pki = pki();
    let ca_pem = pki.server_ca_pem.clone();
    let acceptor = match mode {
        Mode::Plain => None,
        Mode::Tls | Mode::MutualTls => {
            let provider = Arc::new(rustls::crypto::ring::default_provider());
            let builder = rustls::ServerConfig::builder_with_provider(provider.clone())
                .with_safe_default_protocol_versions()
                .unwrap();
            let builder = if matches!(mode, Mode::MutualTls) {
                let mut roots = rustls::RootCertStore::empty();
                roots.add(pki.client_ca_der.clone()).unwrap();
                let verifier = rustls::server::WebPkiClientVerifier::builder_with_provider(Arc::new(roots), provider)
                    .build()
                    .unwrap();
                builder.with_client_cert_verifier(verifier)
            } else {
                builder.with_no_client_auth()
            };
            let key = PrivateKeyDer::Pkcs8(PrivatePkcs8KeyDer::from(pki.server_key.clone()));
            let config = builder.with_single_cert(pki.server_chain.clone(), key).unwrap();
            Some(tokio_rustls::TlsAcceptor::from(Arc::new(config)))
        }
    };
    let recorded = requests.clone();
    tokio::spawn(async move {
        loop {
            let Ok((tcp, _)) = listener.accept().await else { return };
            let (replies, recorded, acceptor) = (replies.clone(), recorded.clone(), acceptor.clone());
            tokio::spawn(async move {
                match acceptor {
                    None => serve(tcp, replies, recorded).await,
                    Some(a) => {
                        if let Ok(tls) = a.accept(tcp).await {
                            serve(tls, replies, recorded).await;
                        }
                    }
                }
            });
        }
    });
    TestServer { addr, requests, ca_pem, pki }
}

async fn serve<S: AsyncRead + AsyncWrite + Unpin>(
    mut s: S,
    replies: Arc<Mutex<VecDeque<Reply>>>,
    recorded: Arc<Mutex<Vec<Recorded>>>,
) {
    let mut buf = Vec::new();
    let mut chunk = [0u8; 4096];
    let head_end = loop {
        if let Some(i) = buf.windows(4).position(|w| w == b"\r\n\r\n") {
            break i;
        }
        match s.read(&mut chunk).await {
            Ok(0) | Err(_) => return,
            Ok(n) => buf.extend_from_slice(&chunk[..n]),
        }
    };
    let head = String::from_utf8_lossy(&buf[..head_end]).to_string();
    let mut lines = head.split("\r\n");
    let request_line = lines.next().unwrap_or_default().to_owned();
    let headers: Vec<(String, String)> =
        lines.filter_map(|l| l.split_once(':').map(|(k, v)| (k.trim().to_owned(), v.trim().to_owned()))).collect();
    let len: usize = headers
        .iter()
        .find(|(k, _)| k.eq_ignore_ascii_case("content-length"))
        .and_then(|(_, v)| v.parse().ok())
        .unwrap_or(0);
    let mut body = buf[head_end + 4..].to_vec();
    while body.len() < len {
        match s.read(&mut chunk).await {
            Ok(0) | Err(_) => return,
            Ok(n) => body.extend_from_slice(&chunk[..n]),
        }
    }
    recorded.lock().unwrap().push(Recorded { request_line, headers, body: String::from_utf8_lossy(&body).to_string() });
    let r = replies.lock().unwrap().pop_front().unwrap_or_else(|| reply(599, r#"{"error":"unscripted"}"#));
    if r.hang {
        // Wait until the client gives up and closes the connection.
        let _ = s.read(&mut chunk).await;
        return;
    }
    let mut out = format!("HTTP/1.1 {} Test\r\nContent-Length: {}\r\nConnection: close\r\n", r.status, r.body.len());
    if !r.body.is_empty() {
        out.push_str("Content-Type: application/json\r\n");
    }
    for (k, v) in &r.headers {
        out.push_str(&format!("{k}: {v}\r\n"));
    }
    out.push_str("\r\n");
    out.push_str(r.body);
    let _ = s.write_all(out.as_bytes()).await;
    let _ = s.shutdown().await;
}
