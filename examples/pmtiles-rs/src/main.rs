//! pmtiles-rs-tile URL Z X Y
//!
//! Prints the stored bytes of one tile to stdout using the `pmtiles` crate's
//! HTTP backend. Exit 0 with no output means the tile is absent; any error
//! exits 1 with the message on stderr. This is the contract of
//! examples/cli-readers/run-scenarios.mjs.
use std::io::Write;

use pmtiles::{AsyncPmTilesReader, HttpBackend, TileCoord};

#[tokio::main(flavor = "current_thread")]
async fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 5 {
        eprintln!("usage: pmtiles-rs-tile URL Z X Y");
        std::process::exit(2);
    }
    if let Err(e) = run(&args[1], &args[2], &args[3], &args[4]).await {
        eprintln!("{e}");
        std::process::exit(1);
    }
}

async fn run(url: &str, z: &str, x: &str, y: &str) -> Result<(), Box<dyn std::error::Error>> {
    let coord = TileCoord::new(z.parse()?, x.parse()?, y.parse()?)?;
    let backend = HttpBackend::try_from(pmtiles::reqwest::Client::new(), url)?;
    let reader = AsyncPmTilesReader::try_from_source(backend).await?;
    if let Some(bytes) = reader.get_tile(coord).await? {
        std::io::stdout().write_all(&bytes)?;
    }
    Ok(())
}
