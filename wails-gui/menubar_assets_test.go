package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"os"
	"testing"
)

func TestAppIconAssetsMatchViolet(t *testing.T) {
	for _, asset := range []struct {
		path   string
		source string
	}{
		{"assets/appicon.png", "app-icon-macos-1024.png"},
		{"build/appicon.png", "app-icon-macos-1024.png"},
		{"../ios/Yansu/Assets.xcassets/AppIcon.appiconset/AppIconOpaque.png", "app-icon-ios-1024.png"},
	} {
		t.Run(asset.path, func(t *testing.T) {
			got, err := os.ReadFile(asset.path)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile("../docs/violet/ds/icons/" + asset.source)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatal("app icon differs from supplied Violet render")
			}
			if asset.path == "assets/appicon.png" && !bytes.Equal(embeddedAppIcon, want) {
				t.Fatal("embedded app icon differs from supplied Violet render")
			}
		})
	}
}

func TestMacOSIconRetinaResolution(t *testing.T) {
	data, err := os.ReadFile("build/darwin/iconfile.icns")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 8 || string(data[:4]) != "icns" || int(binary.BigEndian.Uint32(data[4:8])) != len(data) {
		t.Fatal("invalid ICNS header")
	}
	var retina image.Image
	for offset := 8; offset < len(data); {
		if offset+8 > len(data) {
			t.Fatal("truncated ICNS entry")
		}
		size := int(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		if size < 8 || size > len(data)-offset {
			t.Fatal("invalid ICNS entry size")
		}
		if string(data[offset:offset+4]) == "ic10" {
			retina, err = png.Decode(bytes.NewReader(data[offset+8 : offset+size]))
			if err != nil {
				t.Fatal(err)
			}
		}
		offset += size
	}
	if retina == nil || retina.Bounds().Dx() != 1024 || retina.Bounds().Dy() != 1024 {
		t.Fatal("ICNS must contain a true 1024px Retina representation")
	}
	source, err := png.Decode(bytes.NewReader(embeddedAppIcon))
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 1024; y++ {
		for x := 0; x < 1024; x++ {
			r, g, b, a := retina.At(x, y).RGBA()
			sr, sg, sb, sa := source.At(x, y).RGBA()
			if r != sr || g != sg || b != sb || a != sa {
				t.Fatalf("ICNS Retina pixel (%d, %d) differs from Violet", x, y)
			}
		}
	}
}

func TestMenubarTemplateAssets(t *testing.T) {
	for _, asset := range []struct {
		name   string
		source string
		size   int
	}{
		{"menubar-icon.png", "menubar-template.png", 18},
		{"menubar-icon@2x.png", "menubar-template@2x.png", 36},
		{"menubar-icon@3x.png", "menubar-template@3x.png", 54},
	} {
		t.Run(asset.name, func(t *testing.T) {
			data, err := embeddedMenuBarIcons.ReadFile("assets/" + asset.name)
			if err != nil {
				t.Fatal(err)
			}
			source, err := os.ReadFile("../docs/violet/ds/icons/" + asset.source)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, source) {
				t.Fatal("embedded template differs from the supplied Violet asset")
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds().Dx() != asset.size || img.Bounds().Dy() != asset.size {
				t.Fatalf("template bounds = %v, want %dx%d", img.Bounds(), asset.size, asset.size)
			}
			var visible, transparent bool
			for y := 0; y < asset.size; y++ {
				for x := 0; x < asset.size; x++ {
					r, g, b, a := img.At(x, y).RGBA()
					visible = visible || a > 0
					transparent = transparent || a == 0
					if r != 0 || g != 0 || b != 0 {
						t.Fatalf("template has a non-black pixel at (%d, %d)", x, y)
					}
				}
			}
			if !visible || !transparent {
				t.Fatal("template must contain both the mark and a transparent background")
			}
		})
	}
}

func TestWindowsTrayIconUsesViolet(t *testing.T) {
	data := embeddedAppIconICO
	if len(data) < 6 || binary.LittleEndian.Uint16(data[2:4]) != 1 {
		t.Fatal("invalid ICO header")
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count != 6 || len(data) < 6+16*count {
		t.Fatal("ICO must retain the six tray resolutions")
	}
	for i := 0; i < count; i++ {
		entry := data[6+i*16 : 6+(i+1)*16]
		size := int(binary.LittleEndian.Uint32(entry[8:12]))
		offset := int(binary.LittleEndian.Uint32(entry[12:16]))
		if offset < 0 || size < 0 || offset+size > len(data) {
			t.Fatal("invalid ICO entry")
		}
		img, err := png.Decode(bytes.NewReader(data[offset : offset+size]))
		if err != nil {
			t.Fatal(err)
		}
		// At 16px every stroke is antialiased into the white tile. Larger
		// representations retain fully covered pixels of the source fill.
		var tolerance uint32
		if img.Bounds().Dx() == 16 {
			tolerance = 16 * 257
		}
		near := func(actual, expected uint32) bool {
			if actual > expected {
				return actual-expected <= tolerance
			}
			return expected-actual <= tolerance
		}
		var violet bool
		for y := 0; y < img.Bounds().Dy(); y++ {
			for x := 0; x < img.Bounds().Dx(); x++ {
				r, g, b, a := img.At(x, y).RGBA()
				violet = violet || (near(r, 0x5959) && near(g, 0x6868) && near(b, 0xdfdf) && a == 0xffff)
			}
		}
		if !violet {
			t.Errorf("ICO resolution %d lacks the supplied violet fill", img.Bounds().Dx())
		}
	}
}
