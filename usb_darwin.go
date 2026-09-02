package player

import (
	"github.com/go-macos/iokit/hid"
	"github.com/go-macos/iokit/usb"
	"github.com/go-xrkit/xrkit/glasses"
)

// usbDevices lists the attached devices the glasses could be among, so the pair
// in front of the user is recognised by its PRODUCT id and not merely by its
// brand -- every VITURE panel calls itself "VITURE" on the display side, so the
// product id is the only thing that names a model.
//
// It asks TWICE, because the two enumerations see different things and neither
// is a superset of the other. Walking the USB tree found an XREAL 1S and no
// VITURE at all; asking HID for VITURE's vendor found two VITURE interfaces the
// tree never listed. A headset reached through a dock can enumerate one way and
// not the other, so both are asked and the answers merged.
//
// Nothing is opened, so no Input Monitoring consent is involved -- which also
// means the HID half MUST name a vendor. An empty HID filter matches every
// device, and matching every device opens every device: one keyboard the
// process may not touch and the call fails wholesale, returning nothing. That
// is exactly how this first came back empty.
//
// Failures are not errors here. A machine that cannot enumerate simply leaves
// identification to the display name, which still names a brand.
func usbDevices() []glasses.USB {
	var out []glasses.USB
	seen := make(map[[2]uint16]bool)
	add := func(vendor, product uint16, name string) {
		// One headset publishes several interfaces under the same ids, and they
		// say nothing new: identification reads the ids and the product string.
		key := [2]uint16{vendor, product}
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, glasses.USB{Vendor: vendor, Product: product, Name: name})
	}
	if devs, err := usb.Devices(usb.Filter{}); err == nil {
		for _, d := range devs {
			i := d.Info()
			d.Close()
			add(i.VendorID, i.ProductID, i.Product)
		}
	}
	for _, vendor := range glasses.Vendors() {
		devs, err := hid.Devices(hid.Filter{VendorID: vendor})
		if err != nil {
			continue
		}
		for _, d := range devs {
			i := d.Info()
			d.Close()
			add(i.VendorID, i.ProductID, i.Product)
		}
	}
	return out
}
