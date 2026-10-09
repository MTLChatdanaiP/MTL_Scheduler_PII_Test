// Batch 7 (RFC-009 §22): a div with role="button" must work like a button -- Enter AND Space activate it. Only when the key was
// pressed ON the row itself (a Space typed into an input inside the row must stay a space), and Space must not also scroll the page.
export function isActivateKey(key: string): boolean {
    return key === "Enter" || key === " ";
}

export function activateOnKey(e: KeyboardEvent, action: () => void): void {
    if (e.target !== e.currentTarget || !isActivateKey(e.key)) return;
    e.preventDefault();
    action();
}
