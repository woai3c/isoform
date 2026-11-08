import SwiftUI

extension View {
    /// The keyboard's Done button for a composer field. Chat and Topic share it: Done puts the keyboard away and
    /// keeps the tab bar where it is.
    func composerKeyboardDone(_ dismiss: @escaping () -> Void) -> some View {
        toolbar {
            ToolbarItemGroup(placement: .keyboard) {
                Spacer()
                Button(String(localized: "Done"), action: dismiss)
                    .accessibilityIdentifier("composer.keyboard.done")
            }
        }
    }
}
