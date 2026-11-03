import Foundation

enum DuoMotion {
    static func dockedPlayer(size: CGSize, in bounds: CGRect, foldX: CGFloat? = nil,
                             clearance: CGFloat = DuoLayout.foldClearance) -> CGRect {
        var frame = CGRect(x: bounds.maxX - size.width - clearance,
                           y: bounds.maxY - size.height - clearance,
                           width: size.width, height: size.height)
        if let foldX {
            frame = DuoLayout.clear(frame, ofFoldAt: foldX,
                                    containerWidth: bounds.maxX, clearance: clearance)
        }
        return frame
    }
}
