// lxcode Tauri 壳：窗口生命周期 + __LX__ 桥（前端窗口控制按钮的通道）
// + 后端进程管理（lxcode.exe --serve 的 spawn/kill）。
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::process::{Child, Command};
use std::sync::Mutex;
use tauri::{Manager, WebviewWindow};

// 全局后端进程句柄（壳退出时 kill——防止孤儿后端占着 7789）
static BACKEND: Mutex<Option<Child>> = Mutex::new(None);

/// 启动后端进程：lxcode.exe --serve（从 exe 同级或 ../lxcode.exe 找）
fn spawn_backend() -> Result<(), String> {
    // 先检查 7789 是否已在监听（后端已由服务/手动启动）
    if port_is_listening(7789) {
        println!("[lxcode] 后端已在运行（:7789），跳过 spawn");
        return Ok(());
    }

    // 候选路径：exe 同级 / 上级目录（开发时 target/debug/../..）
    let exe_dir = std::env::current_exe()
        .map_err(|e| e.to_string())?
        .parent()
        .map(|p| p.to_path_buf())
        .ok_or("无法获取 exe 路径")?;

    let candidates: Vec<std::path::PathBuf> = vec![
        exe_dir.join("lxcode-backend.exe"),
        exe_dir.join("lxcode.exe"),
        exe_dir.join("..").join("lxcode-backend.exe"),
        exe_dir.join("..").join("..").join("lxcode.exe"),
        exe_dir.join("..").join("..").join("..").join("lxcode.exe"),
        std::path::PathBuf::from("lxcode-backend.exe"),
    ];

    for candidate in candidates.iter() {
        if candidate.exists() {
            println!("[lxcode] 启动后端: {:?}", candidate);
            // config 也按 exe 同目录找（相对 cwd 不靠谱——Tauri 的 cwd 可能是系统目录）
            let config = candidate.parent().unwrap().join("config").join("local.json");
            let config_str = if config.exists() {
                config.to_string_lossy().to_string()
            } else {
                "config/local.json".to_string() // 退化：走默认路径
            };
            let mut child = Command::new(candidate)
                .arg("--serve")
                .arg("--config")
                .arg(&config_str)
                .spawn()
                .map_err(|e| format!("启动后端失败: {}", e))?;
            *BACKEND.lock().unwrap() = Some(child);
            // 等待端口就绪（后端启动需要几百毫秒）
            for _ in 0..30 {
                if port_is_listening(7789) {
                    println!("[lxcode] 后端就绪");
                    return Ok(());
                }
                std::thread::sleep(std::time::Duration::from_millis(200));
            }
            println!("[lxcode] 警告: 后端启动后 6s 内端口未就绪");
            return Ok(());
        }
    }

    // 找不到后端二进制——不阻塞窗口打开（前端会显示"连接后端失败"）
    println!("[lxcode] 未找到 lxcode.exe 后端二进制（前端将显示连接失败）");
    Ok(())
}

/// 检查端口是否在监听（TCP connect 探测）
fn port_is_listening(port: u16) -> bool {
    std::net::TcpStream::connect(("127.0.0.1", port)).is_ok()
}

fn main() {
    tauri::Builder::default()
        .setup(|app| {
            // 启动后端进程（非阻塞窗口打开——后端慢启动不挡 UI）
            std::thread::spawn(|| {
                if let Err(e) = spawn_backend() {
                    eprintln!("[lxcode] 后端启动错误: {}", e);
                }
            });

            // 注入 __LX__ 桥到前端 window 对象
            if let Some(win) = app.get_webview_window("main") {
                inject_bridge(&win);
            }
            Ok(())
        })
        .on_window_event(|window, event| {
            // 窗口关闭时 kill 后端（壳管理生命周期——不泄漏孤儿进程）
            if let tauri::WindowEvent::Destroyed = event {
                if window.label() == "main" {
                    if let Ok(mut guard) = BACKEND.lock() {
                        if let Some(mut child) = guard.take() {
                            println!("[lxcode] 关闭后端进程");
                            let _ = child.kill();
                        }
                    }
                }
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}

/// 注入 window.__LX__ 桥：前端窗口控制按钮调用 Tauri 的 appWindow API
fn inject_bridge(win: &WebviewWindow) {
    let js = r#"
        (function() {
            const invoke = window.__TAURI_INTERNALS__.invoke;
            window.__LX__ = {
                minimize: () => invoke('plugin:window|minimize', { label: 'main' }),
                toggleMaximize: () => invoke('plugin:window|toggle_maximize', { label: 'main' }),
                close: () => invoke('plugin:window|close', { label: 'main' }),
            };
        })();
    "#;
    let _ = win.eval(js);
}
