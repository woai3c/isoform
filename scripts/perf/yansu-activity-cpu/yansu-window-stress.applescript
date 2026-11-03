use scripting additions

property targetProcessName : "Yansu"
property durationSeconds : 60
property tickDelaySeconds : 0.03
property dragSteps : 3
property modeName : "full"

on randomInt(minValue, maxValue)
    if maxValue < minValue then return minValue
    return (random number from minValue to maxValue) as integer
end randomInt

on joinText(textItems, delimiter)
    set oldDelimiters to AppleScript's text item delimiters
    set AppleScript's text item delimiters to delimiter
    set joinedText to textItems as text
    set AppleScript's text item delimiters to oldDelimiters
    return joinedText
end joinText

on processNamesWithWindows()
    set processNames to {}
    tell application "System Events"
        set candidateProcesses to application processes whose visible is true and background only is false
        repeat with candidateProcess in candidateProcesses
            try
                if (count of windows of candidateProcess) > 0 then
                    set candidateName to name of candidateProcess as text
                    if candidateName is not in processNames then set end of processNames to candidateName
                end if
            end try
        end repeat
    end tell
    return processNames
end processNamesWithWindows

on prepareSwitchStressTargets(activeModeName)
    set processNames to my processNamesWithWindows()
    if activeModeName is "move-only" then return processNames
    if (count of processNames) >= 2 then return processNames

    try
        tell application "Finder"
            activate
            if (count of windows) is 0 then make new Finder window to home
        end tell
    end try
    delay 0.3

    try
        tell application "Calculator" to activate
    end try
    delay 0.7

    return my processNamesWithWindows()
end prepareSwitchStressTargets

on requireStressTargets(processNames, activeModeName)
    set processCount to count of processNames
    if activeModeName is "move-only" then
        if processCount > 0 then return
        error "Window stress found no visible, non-background app windows. Open at least one normal window and retry." number 1001
    end if

    if processCount >= 2 then return

    set candidateText to "<none>"
    if processCount > 0 then set candidateText to my joinText(processNames, ", ")
    error "Window switch stress needs at least two visible, non-background apps with windows. Current candidates: " & candidateText & ". Open another normal app window, such as Finder or Calculator, then retry." number 1001
end requireStressTargets

on chooseDifferentProcess(processNames, previousProcessName)
    if (count of processNames) is 1 then return item 1 of processNames

    repeat 10 times
        set selectedProcessName to item (my randomInt(1, count of processNames)) of processNames
        if selectedProcessName is not previousProcessName then return selectedProcessName
    end repeat

    repeat with candidateProcessName in processNames
        set candidateName to candidateProcessName as text
        if candidateName is not previousProcessName then return candidateName
    end repeat

    return item 1 of processNames
end chooseDifferentProcess

on screenBounds()
    try
        tell application "Finder" to return bounds of window of desktop
    on error
        return {0, 0, 1440, 900}
    end try
end screenBounds

on moveWindowInSteps(processName, windowIndex, targetBounds)
    set {screenLeft, screenTop, screenRight, screenBottom} to targetBounds
    tell application "System Events"
        tell application process processName
            set windowPosition to position of window windowIndex
            set windowSize to size of window windowIndex
            set startX to item 1 of windowPosition
            set startY to item 2 of windowPosition
            set windowWidth to item 1 of windowSize
            set windowHeight to item 2 of windowSize

            set maxX to screenRight - 80
            set maxY to screenBottom - 80
            if windowWidth < (screenRight - screenLeft) then set maxX to screenRight - windowWidth
            if windowHeight < (screenBottom - screenTop) then set maxY to screenBottom - windowHeight

            set targetX to my randomInt(screenLeft, maxX)
            set targetY to my randomInt(screenTop + 24, maxY)
            set didMove to (targetX is not startX) or (targetY is not startY)

            repeat with stepIndex from 1 to dragSteps
                set nextX to startX + (((targetX - startX) * stepIndex) / dragSteps)
                set nextY to startY + (((targetY - startY) * stepIndex) / dragSteps)
                set position of window windowIndex to {nextX as integer, nextY as integer}
                delay 0.005
            end repeat
            return didMove
        end tell
    end tell
end moveWindowInSteps

on stressOneWindow(processName, targetBounds, shouldSwitch, shouldMove)
    set switchedProcess to false
    set movedWindow to false
    tell application "System Events"
        tell application process processName
            if shouldSwitch then
                set frontmost to true
                delay 0.01
                set switchedProcess to frontmost
            end if

            set windowCount to count of windows
            if windowCount is 0 then return {switchedProcess, false}

            set targetWindowIndex to my randomInt(1, windowCount)
            if shouldSwitch then
                try
                    perform action "AXRaise" of window targetWindowIndex
                end try
            end if

            if shouldMove then
                try
                    set movedWindow to my moveWindowInSteps(processName, targetWindowIndex, targetBounds)
                end try
            end if
        end tell
    end tell
    return {switchedProcess, movedWindow}
end stressOneWindow

on run argv
    set activeTargetProcessName to targetProcessName
    set activeDurationSeconds to durationSeconds
    set activeModeName to modeName
    if (count of argv) >= 1 then set activeDurationSeconds to (item 1 of argv) as integer
    if (count of argv) >= 2 then set activeTargetProcessName to (item 2 of argv) as text
    if (count of argv) >= 3 then set activeModeName to (item 3 of argv) as text

    set targetBounds to my screenBounds()
    set processNames to my prepareSwitchStressTargets(activeModeName)
    my requireStressTargets(processNames, activeModeName)
    set moveOnlyProcessName to ""
    set lastSelectedProcessName to ""
    set startedAt to current date
    set stopAt to startedAt + activeDurationSeconds
    set iterations to 0
    set switchedWindows to 0
    set movedWindows to 0
    set moveFailures to 0
    set switchFailures to 0

    repeat while (current date) < stopAt
        if ((iterations mod 25) = 0) then set processNames to my processNamesWithWindows()
        if (count of processNames) is 0 then
            delay tickDelaySeconds
        else
            if activeModeName is "move-only" then
                if moveOnlyProcessName is "" or moveOnlyProcessName is not in processNames then
                    if activeTargetProcessName is in processNames then
                        set moveOnlyProcessName to activeTargetProcessName
                    else
                        set moveOnlyProcessName to item (my randomInt(1, count of processNames)) of processNames
                    end if
                end if
                set selectedProcessName to moveOnlyProcessName
            else
                -- Bias every third interaction toward Yansu while still forcing window switching.
                if ((iterations mod 3) = 0) and (activeTargetProcessName is in processNames) and (activeTargetProcessName is not lastSelectedProcessName) then
                    set selectedProcessName to activeTargetProcessName
                else
                    set selectedProcessName to my chooseDifferentProcess(processNames, lastSelectedProcessName)
                end if
            end if

            try
                set shouldSwitch to activeModeName is not "move-only" or iterations is 0
                set shouldMove to activeModeName is not "switch-only"
                set stressResult to my stressOneWindow(selectedProcessName, targetBounds, shouldSwitch, shouldMove)
                set switchedProcess to item 1 of stressResult
                set movedWindow to item 2 of stressResult

                if shouldSwitch then
                    if switchedProcess and selectedProcessName is not lastSelectedProcessName then
                        set switchedWindows to switchedWindows + 1
                    else
                        set switchFailures to switchFailures + 1
                    end if
                end if
                if shouldMove then
                    if movedWindow then
                        set movedWindows to movedWindows + 1
                    else
                        set moveFailures to moveFailures + 1
                    end if
                end if
                set lastSelectedProcessName to selectedProcessName
            on error
                set switchFailures to switchFailures + 1
            end try

            set iterations to iterations + 1
            delay tickDelaySeconds
        end if
    end repeat

    try
        tell application activeTargetProcessName to activate
    end try

    return "Mode " & activeModeName & ": completed " & switchedWindows & " real app switches in " & activeDurationSeconds & "s; moved " & movedWindows & " windows; " & moveFailures & " move attempts failed; " & switchFailures & " switch/action attempts failed."
end run
