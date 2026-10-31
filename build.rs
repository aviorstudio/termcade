fn main() {
    println!(
        "cargo:rustc-env=TERMCADE_BUILD_TARGET={}",
        std::env::var("TARGET").expect("Cargo target")
    );
}
