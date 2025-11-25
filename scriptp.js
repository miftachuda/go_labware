var ec = {
  name: "ECruiser JavaScript Framework",
  version: "2.0",
  copyright:
    "(C)Copyright 2000-2008 Simplica Corporation; All rights reserved.",
  er: function (_1, _2, _3, _4, _5, _6, _7) {
    var _8 = new Date();
    ec.log.info(
      "Initiating event request; obj = " +
        _1 +
        "; eventId = " +
        _2 +
        "; ajax = " +
        _3 +
        "; urlBase = " +
        _4,
      "ec.er"
    );
    if (ec.submitting) {
      return false;
    }
    var _9 = ec.comp.getComponent(_1.id);
    if (_9 && _9.isDisabled()) {
      return false;
    }
    if (ec.ajax.ACTIVE_REQUEST != null) {
    }
    if (!_1) {
      ec.log.error("The obj argument is required");
      return;
    }
    if (!_2) {
      ec.log.error("The event ID argument is required");
      return;
    }
    var _a = null;
    if (_1.id) {
      _a = _1.id;
    } else {
      if (_1.name) {
        _a = _1.name;
      } else {
        ec.log.error("The source object must have an ID or name");
        return;
      }
    }
    var _b = ec.dom.getParentForm(_1);
    var _c = _4;
    if (!_c) {
      if (_b) {
        _c = _b.action;
      } else {
        _c = window.location.href;
      }
    }
    if (
      _c.indexOf("&") === _c.length - 1 ||
      _c.indexOf("?") === _c.length - 1
    ) {
      _c = _c.substring(0, _c.length - 1);
    }
    _c = ec.util.removeParams(_c, ["ec_eid", "ec_cid"]);
    var _d = _c.indexOf("?") > 0 ? "&" : "?";
    _c =
      _c +
      _d +
      "ec_eid=" +
      ec.bs.encodeURI(_2) +
      "&ec_cid=" +
      ec.bs.encodeURI(_1.id);
    var _e = "";
    if (_5) {
      _e = _5;
    }
    if (_7 && _7.ctrl) {
      if (_7.ctrl.indexOf("&") == 0) {
        _e = _e + _7.ctrl;
      } else {
        _e = _e + "&" + _7.ctrl;
      }
    }
    if (_3 && ec.bs.isAjaxSupported()) {
      ec.makeAjaxRequest(_c, _1, _b, _6, true, true, null, _e, _7.immediate);
    } else {
      ec.makeNormalRequest(_c, _b);
    }
    ec.log.debug(
      "Time for event request = " + (new Date() - _8) + "ms",
      "ec.er"
    );
  },
  erBase: function (id, _10) {
    ec.er(ec.$(id), _10, true);
  },
  makeNormalRequest: function (url, _12) {
    ec.log.debug("ec.makeFullPageRequest url = " + url + "; form = " + _12);
    if (ec.ajax.ACTIVE_REQUEST) {
      var pfs = {};
      pfs.url = url;
      pfs.form = _12;
      ec.ajax.PENDING_FORM_SUBMIT = pfs;
      return;
    }
    if (ec.util.isNull(url)) {
      if (ec.util.isNull(_12)) {
        ec.log.warn(
          "[ec.makeFullPageRequest] Can't make request - both the URL and the form are null"
        );
      } else {
        ec.dom.createHiddenInput(_12, "csrfToken", ec.config.csrfToken);
        _12.submit();
      }
    } else {
      if (ec.util.isNull(_12)) {
        window.location.href = url;
      } else {
        var _14 = _12.action;
        _12.action = url;
        setTimeout(function () {
          ec.dom.createHiddenInput(_12, "csrfToken", ec.config.csrfToken);
          _12.submit();
          _12.action = _14;
        }, 1);
      }
    }
    ec.submitting = true;
  },
  makeAjaxRequest: function (url, _16, _17, _18, _19, _1a, _1b, _1c, _1d) {
    if (ec.log.level >= ec.log.DEBUG) {
      ec.log.debug(
        "Making AJAX request;<br>&nbsp;&nbsp; url = " +
          url +
          ";<br>&nbsp;&nbsp;  domObj = " +
          _16 +
          ";<br>&nbsp;&nbsp;  form = " +
          _17 +
          ";<br>&nbsp;&nbsp;  ajaxStatusId = " +
          _18 +
          ";<br>&nbsp;&nbsp;  enqueue = " +
          _19 +
          ";<br>&nbsp;&nbsp;  async = " +
          _1a +
          ";<br>&nbsp;&nbsp;  listener = " +
          _1b +
          ";<br>&nbsp;&nbsp;  params = " +
          _1c +
          ";<br>&nbsp;&nbsp;  immediate = " +
          _1d,
        "ec.makeAjaxRequest"
      );
    }
    if (ec.submitting) {
      return null;
    }
    if (!_19 && ec.ajax.ACTIVE_REQUEST) {
      return null;
    }
    var req = new ec.ajax.Request(url, _16, _17, _1a, _18, _1c, _1d);
    if (_1b) {
      req.addListener(_1b);
    }
    req.addListener({
      onComplete: function (req) {
        ec.ajax.submitPendingForm();
      },
    });
    if (_19) {
      ec.ajax.enqueueRequest(req);
    } else {
      ec.ajax.makeRequest(req);
    }
    return req;
  },
  makeDomNodeAjaxRequest: function (_20, _21, _22, _23, _24, _25, _26) {
    var _27 = ec.dom.getParentForm(_20);
    var url = "";
    if (_27) {
      url = _27.action;
    } else {
      url = window.location.toString();
    }
    return ec.makeAjaxRequest(url, _20, _27, _22, _23, _24, _25, _21, _26);
  },
  $: function (id) {
    var _2a = null;
    if (typeof id != "string") {
      _2a = id;
    } else {
      _2a = document.getElementById(id);
    }
    return _2a;
  },
};
