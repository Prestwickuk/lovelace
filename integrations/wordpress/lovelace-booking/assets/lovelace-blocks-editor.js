(function (wp) {
  "use strict";
  if (!wp || !wp.blocks || !wp.element || !wp.components) return;

  var el = wp.element.createElement;
  var Fragment = wp.element.Fragment;
  var registerBlockType = wp.blocks.registerBlockType;
  var InspectorControls = wp.blockEditor
    ? wp.blockEditor.InspectorControls
    : wp.editor.InspectorControls;
  var PanelBody = wp.components.PanelBody;
  var TextControl = wp.components.TextControl;
  var SelectControl = wp.components.SelectControl;
  var RangeControl = wp.components.RangeControl;
  var ServerSideRender = wp.serverSideRender;

  // 1. Full Live Booking Section
  registerBlockType("lovelace/booking-widget", {
    title: "Lovelace Live Booking Section",
    description:
      "Embeds your property's interactive availability search, pricing, extras, and guest booking form.",
    icon: "calendar-alt",
    category: "lovelace-booking",
    edit: function (props) {
      var attrs = props.attributes;
      return el(
        Fragment,
        null,
        el(
          InspectorControls,
          null,
          el(
            PanelBody,
            { title: "Booking Widget Settings", initialOpen: true },
            el(TextControl, {
              label: "Property Slug Override (optional)",
              help: "Leave blank to use the default slug in Settings → Lovelace Booking.",
              value: attrs.property || "",
              onChange: function (v) {
                props.setAttributes({ property: v });
              },
            }),
            el(TextControl, {
              label: "Filter Category (optional, e.g. GLAMPING)",
              value: attrs.category || "",
              onChange: function (v) {
                props.setAttributes({ category: v });
              },
            }),
            el(TextControl, {
              label: "Pre-select Accommodation Slug (optional)",
              value: attrs.unit_type || "",
              onChange: function (v) {
                props.setAttributes({ unit_type: v });
              },
            }),
            el(RangeControl, {
              label: "Default Adults",
              min: 1,
              max: 12,
              value: attrs.adults || 2,
              onChange: function (v) {
                props.setAttributes({ adults: v });
              },
            }),
            el(RangeControl, {
              label: "Minimum Height (px)",
              min: 480,
              max: 1200,
              step: 20,
              value: attrs.min_height || 680,
              onChange: function (v) {
                props.setAttributes({ min_height: v });
              },
            })
          )
        ),
        el(ServerSideRender, {
          block: "lovelace/booking-widget",
          attributes: attrs,
        })
      );
    },
    save: function () {
      return null;
    },
  });

  // 2. Availability Search Bar
  registerBlockType("lovelace/search-bar", {
    title: "Lovelace Availability Search Bar",
    description:
      "Drop-in Check-in / Check-out / Guests bar for hero headers, banners, or sidebars.",
    icon: "search",
    category: "lovelace-booking",
    edit: function (props) {
      var attrs = props.attributes;
      return el(
        Fragment,
        null,
        el(
          InspectorControls,
          null,
          el(
            PanelBody,
            { title: "Search Bar Settings", initialOpen: true },
            el(SelectControl, {
              label: "Layout",
              value: attrs.layout || "horizontal",
              options: [
                { label: "Horizontal Bar", value: "horizontal" },
                { label: "Compact 2-Column Card", value: "compact" },
              ],
              onChange: function (v) {
                props.setAttributes({ layout: v });
              },
            }),
            el(SelectControl, {
              label: "When Guest Clicks Search",
              value: attrs.target || "modal",
              options: [
                { label: "Open Booking Modal Overlay", value: "modal" },
                { label: "Navigate in Current Tab", value: "_top" },
                { label: "Open in New Tab", value: "_blank" },
              ],
              onChange: function (v) {
                props.setAttributes({ target: v });
              },
            }),
            el(TextControl, {
              label: "Button Text",
              value: attrs.button_text || "Check Availability",
              onChange: function (v) {
                props.setAttributes({ button_text: v });
              },
            }),
            el(TextControl, {
              label: "Property Slug Override (optional)",
              value: attrs.property || "",
              onChange: function (v) {
                props.setAttributes({ property: v });
              },
            })
          )
        ),
        el(ServerSideRender, {
          block: "lovelace/search-bar",
          attributes: attrs,
        })
      );
    },
    save: function () {
      return null;
    },
  });

  // 3. Branded "Book Direct" Button
  registerBlockType("lovelace/book-button", {
    title: 'Lovelace "Book Direct" Button',
    description:
      "Branded call-to-action button opening an inline booking modal or your direct booking page.",
    icon: "tickets-alt",
    category: "lovelace-booking",
    edit: function (props) {
      var attrs = props.attributes;
      return el(
        Fragment,
        null,
        el(
          InspectorControls,
          null,
          el(
            PanelBody,
            { title: "Book Button Settings", initialOpen: true },
            el(TextControl, {
              label: "Button Label",
              placeholder: "Book Direct",
              value: attrs.label || "",
              onChange: function (v) {
                props.setAttributes({ label: v });
              },
            }),
            el(SelectControl, {
              label: "Click Behaviour",
              value: attrs.mode || "modal",
              options: [
                { label: "Open Booking Modal on Page", value: "modal" },
                { label: "Open in New Tab", value: "new_tab" },
                { label: "Open in Current Tab", value: "same_tab" },
              ],
              onChange: function (v) {
                props.setAttributes({ mode: v });
              },
            }),
            el(TextControl, {
              label: "Specific Accommodation Slug (optional)",
              help: "e.g. shepherds-hut to book a specific unit category.",
              value: attrs.unit_type || "",
              onChange: function (v) {
                props.setAttributes({ unit_type: v });
              },
            }),
            el(TextControl, {
              label: "Property Slug Override (optional)",
              value: attrs.property || "",
              onChange: function (v) {
                props.setAttributes({ property: v });
              },
            })
          )
        ),
        el(ServerSideRender, {
          block: "lovelace/book-button",
          attributes: attrs,
        })
      );
    },
    save: function () {
      return null;
    },
  });

  // 4. SEO Accommodation Cards Grid
  registerBlockType("lovelace/accommodation-grid", {
    title: "Lovelace Accommodation Cards",
    description:
      "Server-rendered, SEO-indexable accommodation cards synced from your Lovelace catalog.",
    icon: "admin-home",
    category: "lovelace-booking",
    edit: function (props) {
      var attrs = props.attributes;
      return el(
        Fragment,
        null,
        el(
          InspectorControls,
          null,
          el(
            PanelBody,
            { title: "Accommodation Grid Settings", initialOpen: true },
            el(SelectControl, {
              label: "Columns",
              value: String(attrs.columns || 2),
              options: [
                { label: "2 Columns", value: "2" },
                { label: "3 Columns", value: "3" },
              ],
              onChange: function (v) {
                props.setAttributes({ columns: Number(v) });
              },
            }),
            el(TextControl, {
              label: "Filter Category (optional, e.g. GLAMPING)",
              value: attrs.category || "",
              onChange: function (v) {
                props.setAttributes({ category: v });
              },
            }),
            el(SelectControl, {
              label: "Card Button Behaviour",
              value: attrs.button_mode || "modal",
              options: [
                { label: "Open Booking Modal Overlay", value: "modal" },
                { label: "Open Booking Page in New Tab", value: "link" },
              ],
              onChange: function (v) {
                props.setAttributes({ button_mode: v });
              },
            }),
            el(SelectControl, {
              label: "Include LodgingBusiness JSON-LD Schema",
              value: attrs.schema || "true",
              options: [
                { label: "Yes (Recommended for SEO)", value: "true" },
                { label: "No", value: "false" },
              ],
              onChange: function (v) {
                props.setAttributes({ schema: v });
              },
            }),
            el(TextControl, {
              label: "Property Slug Override (optional)",
              value: attrs.property || "",
              onChange: function (v) {
                props.setAttributes({ property: v });
              },
            })
          )
        ),
        el(ServerSideRender, {
          block: "lovelace/accommodation-grid",
          attributes: attrs,
        })
      );
    },
    save: function () {
      return null;
    },
  });
})(window.wp);
