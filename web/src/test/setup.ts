import "@testing-library/react";

/**
 * jsdom has no <dialog> behaviour.
 *
 * It parses the element and exposes the `open` property, but `showModal()` and `close()`
 * are simply absent — calling one throws "el.showModal is not a function" from inside a
 * passive effect, which surfaces as an unhandled exception rather than a failed
 * assertion, several frames away from the component that rendered the dialog.
 *
 * Nothing in the suite rendered a Dialog until the subscriber drawer arrived, which is
 * why this was not needed before. The shim is deliberately minimal: it toggles `open` and
 * fires the two events the components actually listen for. It does not emulate the focus
 * trap, inertness or ::backdrop, none of which a jsdom test can meaningfully assert —
 * those are the browser behaviours Dialog uses a native <dialog> to get for free, and
 * verifying them belongs in a real browser.
 */
const dialog = globalThis.HTMLDialogElement?.prototype;
if (dialog && !dialog.showModal) {
  dialog.showModal = function showModal(this: HTMLDialogElement) {
    this.open = true;
  };
  dialog.show = function show(this: HTMLDialogElement) {
    this.open = true;
  };
  dialog.close = function close(this: HTMLDialogElement, returnValue?: string) {
    if (!this.open) return;
    this.open = false;
    if (returnValue !== undefined) this.returnValue = returnValue;
    // Both, and in this order, matching the spec: a component may cancel on one and tidy
    // up on the other.
    this.dispatchEvent(new Event("cancel", { cancelable: true }));
    this.dispatchEvent(new Event("close"));
  };
}
