(function () {
  function setNodeExpanded(node, expanded) {
    node.dataset.open = expanded ? "true" : "false";

    var button = node.querySelector("[data-crd-toggle-node]");
    var content = node.querySelector(".crd-viewer__content");

    if (button) {
      button.setAttribute("aria-expanded", expanded ? "true" : "false");
    }

    if (content) {
      if (expanded) {
        content.hidden = false;
        var height = content.scrollHeight;
        if (height === 0) {
          content.style.maxHeight = "none";
          return;
        }
        content.style.maxHeight = "0px";
        content.offsetHeight; /* force reflow */
        content.style.maxHeight = height + "px";
        content.addEventListener(
          "transitionend",
          function handler() {
            content.style.maxHeight = "none";
            content.removeEventListener("transitionend", handler);
          },
          { once: true }
        );
      } else {
        var height = content.scrollHeight;
        if (height === 0) {
          content.hidden = true;
          content.style.maxHeight = "";
          return;
        }
        content.style.maxHeight = height + "px";
        content.offsetHeight; /* force reflow */
        content.style.maxHeight = "0px";
        content.addEventListener(
          "transitionend",
          function handler() {
            content.hidden = true;
            content.style.maxHeight = "";
            content.removeEventListener("transitionend", handler);
          },
          { once: true }
        );
      }
    }
  }

  function setNodeExpandedImmediate(node, expanded) {
    node.dataset.open = expanded ? "true" : "false";
    var button = node.querySelector("[data-crd-toggle-node]");
    var content = node.querySelector(".crd-viewer__content");
    if (button) {
      button.setAttribute("aria-expanded", expanded ? "true" : "false");
    }
    if (content) {
      content.hidden = !expanded;
      content.style.maxHeight = "";
    }
  }

  function syncButton(viewer) {
    var button = viewer.querySelector("[data-crd-toggle-all]");
    if (!button) {
      return;
    }

    var nodes = Array.prototype.slice.call(
      viewer.querySelectorAll("[data-crd-node]")
    );
    var allExpanded =
      nodes.length > 0 &&
      nodes.every(function (node) {
        return node.dataset.open === "true";
      });

    button.dataset.expanded = allExpanded ? "true" : "false";
    button.textContent = allExpanded ? "Collapse All" : "Expand All";
  }

  function initViewer(viewer) {
    if (viewer.dataset.crdViewerReady === "true") {
      return;
    }

    viewer.dataset.crdViewerReady = "true";
    var button = viewer.querySelector("[data-crd-toggle-all]");
    var nodes = Array.prototype.slice.call(
      viewer.querySelectorAll("[data-crd-node]")
    );

    nodes.forEach(function (node) {
      setNodeExpandedImmediate(node, node.dataset.open === "true");
      var nodeButton = node.querySelector("[data-crd-toggle-node]");
      if (nodeButton) {
        nodeButton.addEventListener("click", function () {
          setNodeExpanded(node, node.dataset.open !== "true");
          syncButton(viewer);
        });
      }
    });

    if (button) {
      button.addEventListener("click", function () {
        var expand = button.dataset.expanded !== "true";
        viewer.querySelectorAll("[data-crd-node]").forEach(function (node) {
          setNodeExpandedImmediate(node, expand);
        });
        syncButton(viewer);
      });
    }

    /* Collapsible mode: click header to toggle */
    if ("crdCollapsible" in viewer.dataset) {
      var header = viewer.querySelector(".crd-viewer__header");
      if (header) {
        header.addEventListener("click", function (e) {
          if (e.target.closest("[data-crd-toggle-all]")) return;
          if (viewer.dataset.crdCollapsed === "true") {
            delete viewer.dataset.crdCollapsed;
          } else {
            viewer.dataset.crdCollapsed = "true";
          }
        });
      }
    }

    syncButton(viewer);
  }

  function navigateToHash() {
    var hash = location.hash;
    if (!hash || hash.length < 2) return;

    var target;
    try {
      target = document.querySelector(hash);
    } catch (_) {
      return;
    }
    if (!target) return;

    var viewer = target.closest("[data-crd-viewer-root]");
    if (!viewer || viewer.dataset.crdViewerReady !== "true") return;

    if (viewer.dataset.crdCollapsed === "true") {
      delete viewer.dataset.crdCollapsed;
    }

    var ancestor = target.parentElement;
    while (ancestor && ancestor !== viewer) {
      if (ancestor.hasAttribute("data-crd-node")) {
        setNodeExpandedImmediate(ancestor, true);
      }
      ancestor = ancestor.parentElement;
    }

    var ownNode = target.querySelector(":scope > [data-crd-node]");
    if (ownNode) {
      setNodeExpandedImmediate(ownNode, true);
    }

    syncButton(viewer);

    target.scrollIntoView({ behavior: "smooth", block: "center" });

    var row =
      target.querySelector(":scope > .crd-viewer__node > .crd-viewer__row") ||
      target.querySelector(":scope > .crd-viewer__row");
    if (row) {
      row.classList.add("crd-viewer__row--highlight");
      function clearHighlight(e) {
        if (e.target.closest(".crd-viewer__row") && e.target.closest(".crd-viewer__row") !== row) {
          row.classList.remove("crd-viewer__row--highlight");
          viewer.removeEventListener("mouseover", clearHighlight);
        }
      }
      viewer.addEventListener("mouseover", clearHighlight);
    }
  }

  function init(root) {
    root.querySelectorAll("[data-crd-viewer-root]").forEach(initViewer);
    navigateToHash();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", function () {
      init(document);
    });
  } else {
    init(document);
  }

  window.addEventListener("hashchange", navigateToHash);

  document.addEventListener("click", function (e) {
    var anchor = e.target.closest(".crd-viewer__anchor");
    if (!anchor) return;
    var href = anchor.getAttribute("href");
    if (!href || href.charAt(0) !== "#") return;
    e.preventDefault();
    history.replaceState(null, "", href);
    navigateToHash();

    var url = location.origin + location.pathname + href;
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(url);
    } else {
      var ta = document.createElement("textarea");
      ta.value = url;
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      document.execCommand("copy");
      ta.remove();
    }
  });

  if (
    typeof document$ !== "undefined" &&
    typeof document$.subscribe === "function"
  ) {
    document$.subscribe(function () {
      init(document);
    });
  }

  window.__crdViewerInit = init;
})();
