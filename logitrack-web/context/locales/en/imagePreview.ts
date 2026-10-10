// Image preview and ZIP download text shared by the accounting pages and the driver monitor
// (`accounting.preview.*`, `accounting.error.*`). It sits in the base dictionary so a route outside the
// accounting group never loads the `accounting` namespace for it (developer-spec.md §10.11, TW4).
const imagePreview: Record<string, string> = {
    "accounting.preview.zoomIn": "Zoom in",
    "accounting.preview.zoomOut": "Zoom out",
    "accounting.preview.resetZoom": "Reset zoom",
    "accounting.preview.previous": "Previous",
    "accounting.preview.next": "Next",
    "accounting.preview.notPreviewable": "This file cannot be previewed here. Open in a new tab instead.",
    "accounting.preview.print": "Print",
    "accounting.preview.downloadZip": "Download ZIP",
    "accounting.preview.downloadZipLoading": "Building ZIP…",

    "accounting.error.zipFailed": "Could not create the ZIP. No images could be downloaded.",
    "accounting.error.partialDownload": "ZIP saved with {added} image(s). {failed} could not be fetched (network or Storage CORS).",
    "accounting.error.corsHint": "If downloads fail but images show in the app, set Storage bucket CORS for your web origin. See docs/FIREBASE_STORAGE_CORS.md.",
};

export default imagePreview;
