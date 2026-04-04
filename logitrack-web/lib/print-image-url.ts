/**
 * Opens print dialog for a remote image URL using a hidden iframe (avoids blank about:blank tabs from window.open + document.write).
 */
export function printImageUrl(url: string): void {
    if (!url.trim()) return;

    const iframe = document.createElement("iframe");
    iframe.setAttribute("aria-hidden", "true");
    iframe.setAttribute("title", "print");
    Object.assign(iframe.style, {
        position: "fixed",
        left: "0",
        top: "0",
        width: "100vw",
        height: "100vh",
        opacity: "0",
        pointerEvents: "none",
        zIndex: "2147483646",
        border: "none",
    });

    const cleanup = () => {
        iframe.remove();
    };

    document.body.appendChild(iframe);

    const win = iframe.contentWindow;
    const doc = win?.document;
    if (!win || !doc) {
        cleanup();
        window.open(url, "_blank", "noopener,noreferrer");
        return;
    }

    doc.open();
    doc.write(
        "<!DOCTYPE html><html><head><meta charset='utf-8'><title>Print</title>" +
            "<style>@page{margin:12mm}html,body{margin:0;min-height:100vh}body{display:flex;justify-content:center;align-items:center;box-sizing:border-box}img{max-width:100%;max-height:100vh;object-fit:contain}</style></head><body></body></html>"
    );
    doc.close();

    const img = doc.createElement("img");
    img.alt = "";
    img.src = url;

    const runPrint = () => {
        requestAnimationFrame(() => {
            requestAnimationFrame(() => {
                let cleaned = false;
                let fallbackTimer: number;
                const finish = () => {
                    if (cleaned) return;
                    cleaned = true;
                    window.clearTimeout(fallbackTimer);
                    win.removeEventListener("afterprint", finish);
                    cleanup();
                };
                fallbackTimer = window.setTimeout(finish, 30_000);
                win.addEventListener("afterprint", finish);
                try {
                    win.focus();
                    win.print();
                } catch {
                    finish();
                }
            });
        });
    };

    img.onerror = () => {
        cleanup();
        window.open(url, "_blank", "noopener,noreferrer");
    };

    doc.body.appendChild(img);
    if (img.complete && img.naturalWidth > 0) {
        runPrint();
    } else {
        img.onload = () => runPrint();
    }
}
