//go:build windows

package main

import (
	"fmt"
	"os/exec"
)

func showWarning(warning screenTimeWarning) error {
	script := fmt.Sprintf(`Add-Type -AssemblyName PresentationFramework
$window = New-Object Windows.Window
$window.Width = 320
$window.Height = 92
$window.WindowStyle = 'None'
$window.ResizeMode = 'NoResize'
$window.ShowInTaskbar = $false
$window.Topmost = $true
$window.AllowsTransparency = $true
$window.Opacity = 0.96
$window.Background = [Windows.Media.BrushConverter]::new().ConvertFromString('%s')
$window.Left = [System.Windows.SystemParameters]::WorkArea.Right - $window.Width - 20
$window.Top = [System.Windows.SystemParameters]::WorkArea.Bottom - $window.Height - 20
$text = New-Object Windows.Controls.TextBlock
$text.Text = '%s'
$text.Foreground = [Windows.Media.Brushes]::White
$text.FontSize = 22
$text.FontWeight = 'SemiBold'
$text.VerticalAlignment = 'Center'
$text.HorizontalAlignment = 'Center'
$window.Content = $text
$timer = New-Object Windows.Threading.DispatcherTimer
$timer.Interval = [TimeSpan]::FromMilliseconds(4500)
$timer.add_Tick({
  $timer.Stop()
  $fade = New-Object Windows.Media.Animation.DoubleAnimation(0.96, 0, [TimeSpan]::FromMilliseconds(500))
  $fade.add_Completed({ $window.Close() })
  $window.BeginAnimation([Windows.Window]::OpacityProperty, $fade)
})
$window.add_Loaded({ $timer.Start() })
[void]$window.ShowDialog()`, warning.color, warning.message)
	return exec.Command("powershell.exe", "-NoProfile", "-WindowStyle", "Hidden", "-Command", script).Start()
}
