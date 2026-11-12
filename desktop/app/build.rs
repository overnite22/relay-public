fn main() {
    let config = slint_build::CompilerConfiguration::new().with_style("fluent".into());
    slint_build::compile_with_config("ui/app.slint", config).expect("Slint UI compiles");

    // Icon and version information in the executable (MSVC toolchain, CI).
    if std::env::var("CARGO_CFG_TARGET_ENV").as_deref() == Ok("msvc") {
        let mut res = winresource::WindowsResource::new();
        res.set_icon("icons/icon.ico")
            .set("ProductName", "Relay")
            .set("FileDescription", "Relay – gestionnaire de connexions à distance")
            .set("LegalCopyright", "Relay contributors – Apache-2.0");
        res.compile().expect("Windows resources compile");
    }
}
