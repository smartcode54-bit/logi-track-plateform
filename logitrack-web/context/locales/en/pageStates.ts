export default {
    // UI3D (#143): the states a page can be in besides its content, and the shared page header. In the base
    // dictionary: the shell renders them on every route, before any split namespace loads.

    // A path with no page (app/app/not-found.tsx; the edge gate answers an unmapped path with it, HTTP 404).
    "pageState.notFound.title": "Page not found",
    "pageState.notFound.body": "There is no page at this address. The link may be mistyped, or the page may have moved.",
    "pageState.goHome": "Go to my home page",
    // A page that crashed while rendering (app/app/error.tsx): the shell stays, the page can be tried again.
    "pageState.error.title": "This page stopped working",
    "pageState.error.body":
        "Something went wrong while showing this page. Try again. If it keeps happening, tell support what you were doing and quote the reference below.",
    "pageState.retry": "Try again",
    "pageState.reference": "Reference for support: {id}",
    // One record of a detail or edit page (components/record-state.tsx). Go answers 404 both for a record that
    // does not exist and for one outside the reader's scope, so the text never says which.
    "pageState.record.notFound.title": "Not found",
    "pageState.record.notFound.body": "This record does not exist or is not in your scope.",
    "pageState.record.backTo": "Back to {page}",
    "pageState.record.loadError.title": "Could not load this record",

    // The page header (components/page-header.tsx).
    "pageHeader.back": "Back to {page}",
    // A row's action menu (an icon-only button names whose actions it opens).
    "common.actionsFor": "Actions for {name}",
    "common.actionsForRow": "Actions for this row",
    // Names of icon-only buttons (DESIGN.md: every icon-only control is named in both languages).
    "common.previousPage": "Previous page",
    "common.nextPage": "Next page",
    "common.removeFile": "Remove file",
    "common.removePhoto": "Remove photo",
    "common.removeRow": "Remove this row",
};
