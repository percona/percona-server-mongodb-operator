(function($, undefined){

  // Expose `TimelineSetter` globally, so we can call `Timeline.Timeline.boot()`
  // to kick off at any point.
  var TimelineSetter = window.TimelineSetter = (window.TimelineSetter || {});

  // Current version of `TimelineSetter`
  TimelineSetter.VERSION = "0.3.2";

  // Mixins
  // ------
  // Each mixin operates on an object's `prototype`.

  // The `observable` mixin adds simple event notifications to the passed in
  // object. Unlike other notification systems, when an event is triggered every
  // callback bound to the object is invoked.
  var observable = function(obj){
    // Registers a callback function for notification at a later time.
    obj.bind = function(e, cb){
      var callbacks = (this._callbacks = this._callbacks || {});
      var list = (callbacks[e] = callbacks[e] || []);
      list.push(cb);
    };

    // Invoke all callbacks registered to the object with `bind`.
    obj.trigger = function(e){
      if(!this._callbacks) return;
      var list = this._callbacks[e];
      if(!list) return;
      for(var i = 0; i < list.length; i++) list[i].apply(this, arguments);
    };
  };


  // Each `transformable` contains two event listeners that handle moving associated
  // DOM elements around the page.
  var transformable = function(obj){
    // Move the associated element a specified delta.
    obj.move = function(evtName, e){
      if (!e.deltaX) return;
      if (_.isUndefined(this.currOffset)) this.currOffset = 0;
      this.currOffset += e.deltaX;
      this.el.css({"left" : this.currOffset});
    };

    // The width for the `Bar` and `CardContainer` objects is set in percentages,
    // in order to zoom the Timeline all that's needed is to increase or decrease
    // the percentage width.
    obj.zoom = function(evtName, e){
      if (!e.width) return;
      this.el.css({ "width": e.width });
    };
  };

  // The `queryable` mixin scopes jQuery to
  // a given container.
  var queryable = function(obj, container) {
    obj.$ = function(query) {
      return window.$(query, container);
    };
  };


  // Plugins
  // -------
  // Each plugin operates on an instance of an object.

  // Check to see if we're on a mobile device.
  var touchInit = 'ontouchstart' in document;
  if (touchInit) $.event.props.push("touches");

  // The `draggable` plugin tracks changes in X offsets due to mouse movement
  // or finger gestures and proxies associated events on a particular element.
  // Most of this is inspired by polymaps.
  var draggable = function(obj){
    var drag;

    // Start tracking deltas due to a tap or single click.
    function mousedown(e){
      e.preventDefault();
      drag = {x: e.pageX};
      e.type = "dragstart";
      obj.el.trigger(e);
    }

    // The user is interacting; capture the offset and trigger a `dragging` event.
    function mousemove(e){
      if (!drag) return;
      e.preventDefault();
      e.type = "dragging";
      e = _.extend(e, {
        deltaX: (e.pageX || e.touches[0].pageX) - drag.x
      });
      drag = { x: (e.pageX || e.touches[0].pageX) };
      obj.el.trigger(e);
    }

    // We're done tracking the movement set drag back to `null` for the next event.
    function mouseup(e){
      if (!drag) return;
      drag = null;
      e.type = "dragend";
      obj.el.trigger(e);
    }

    if (!touchInit) {
      // Bind on mouse events if we have a mouse...
      obj.el.bind("mousedown", mousedown);

      $(document).bind("mousemove", mousemove);
      $(document).bind("mouseup", mouseup);
    } else {
      // otherwise capture `touchstart` events in order to simulate `doubletap` events.
      var last;
      obj.el.bind("touchstart", function(e) {
        var now = Date.now();
        var delta = now - (last || now);
        var type = delta > 0 && delta <= 250 ? "doubletap" : "tap";
        drag = {x: e.touches[0].pageX};
        last = now;
        obj.el.trigger($.Event(type));
      });

      obj.el.bind("touchmove", mousemove);
      obj.el.bind("touchend", mouseup);
    }
  };


  // Older versions of safari fire incredibly huge mousewheel deltas. We'll need
  // to dampen the effects.
  var safari = /WebKit\/533/.test(navigator.userAgent);

  // The `wheel` plugin captures events triggered by mousewheel, and dampen the
  // `delta` if running in Safari.
  var wheel = function(obj){
    function mousewheel(e){
      e.preventDefault();
      var delta = (e.wheelDelta || -e.detail);
      if (safari){
        var negative = delta < 0 ? -1 : 1;
        delta = Math.log(Math.abs(delta)) * negative * 2;
      }
      e.type = "scrolled";
      e.deltaX = delta;
      obj.el.trigger(e);
    }

    obj.el.bind("mousewheel DOMMouseScroll", mousewheel);
  };

  // Utilities
  // -----

  // A utility class for storing the extent of the timeline.
  var Bounds = function(){
    this.min = +Infinity;
    this.max = -Infinity;
  };

  Bounds.prototype.extend = function(num){
    this.min = Math.min(num, this.min);
    this.max = Math.max(num, this.max);
  };

  Bounds.prototype.width = function(){
    return this.max - this.min;
  };

  // Translate a particular number from the current bounds to a given range.
  Bounds.prototype.project = function(num, max){
    return (num - this.min) / this.width() * max;
  };


  // `Intervals` is a particularly focused class to calculate even breaks based
  // on the passed in `Bounds`.
  var Intervals = function(bounds, interval) {
    this.max = bounds.max;
    this.min = bounds.min;

    if(!interval || !this.INTERVALS[interval]) {
      var i = this.computeMaxInterval();
      this.maxInterval = this.INTERVAL_ORDER[i];
      this.idx = i;
    } else {
      this.maxInterval = interval;
      this.idx = _.indexOf(this.INTERVAL_ORDER, interval);
    }
  };

  // Format dates based for AP style.
  // Pass an override function in the config object to override.
  Intervals.dateFormats = function(timestamp) {
    var d = new Date(timestamp);
    var defaults = {};
    var months   = ['Jan.', 'Feb.', 'March', 'April', 'May', 'June', 'July', 'Aug.', 'Sept.', 'Oct.', 'Nov.', 'Dec.'];
    var bigHours = d.getHours() > 12;
    var ampm     = " " + (d.getHours() >= 12 ? 'p.m.' : 'a.m.');


    defaults.month = months[d.getMonth()];
    defaults.year  = d.getFullYear();
    defaults.date  = defaults.month + " " + d.getDate() + ', ' + defaults.year;

    var hours;
    if(bigHours) {
      hours = d.getHours() - 12;
    } else {
      hours = d.getHours() > 0 ? d.getHours() : "12";
    }

    hours += ":" + padNumber(d.getMinutes());
    defaults.hourWithMinutes = hours + ampm;
    defaults.hourWithMinutesAndSeconds = hours + ":" + padNumber(d.getSeconds()) + ampm;
    // If we have user overrides, set them to defaults.
    return Intervals.formatter(d, defaults) || defaults;
  };

  // A utility function to format dates in AP Style.
  Intervals.dateStr = function(timestamp, interval) {
    var d = new Intervals.dateFormats(timestamp);
    switch (interval) {
      case "Millennium":
        return d.year;
      case "Quincentenary":
        return d.year;
      case "Century":
        return d.year;
      case "HalfCentury":
        return d.year;
      case "Decade":
        return d.year;
      case "Lustrum":
        return d.year;
      case "FullYear":
        return d.year;
      case "Month":
        return d.month + ', ' + d.year;
      case "Week":
        return d.date;
      case "Date":
        return d.date;
      case "Hours":
        return d.hourWithMinutes;
      case "HalfHour":
        return d.hourWithMinutes;
      case "QuarterHour":
        return d.hourWithMinutes;
      case "Minutes":
        return d.hourWithMinutes;
      case "Seconds":
        return d.hourWithMinutesAndSeconds;
    }
  };

  Intervals.prototype = {
    // Sane estimates of date ranges for the `isAtLeastA` test.
    INTERVALS : {
      Millennium    : 69379200000000,  // 2200 years is the trigger
      Quincentenary : 34689600000000, // 1100 years is the trigger
      Century       : 9460800000000,  // 300 years is the trigger
      HalfCentury   : 3153600000000,  // 100 years is the trigger
      Decade        : 315360000000,
      Lustrum       : 157680000000,
      FullYear      : 31536000000,
      Month         : 2592000000,
      Week          : 604800000,
      Date          : 86400000,
      Hours         : 3600000,
      HalfHour      : 1800000,
      QuarterHour   : 900000,
      Minutes       : 60000,
      Seconds       : 1000 // 1,000 millliseconds equals on second
    },

    // The order used when testing where exactly a timespan falls.
    INTERVAL_ORDER : [
        'Seconds',
        'Minutes',
        'QuarterHour',
        'HalfHour',
        'Hours',
        'Date',
        'Week',
        'Month',
        'FullYear',
        'Lustrum',
        'Decade',
        'HalfCentury',
        'Century',
        'Quincentenary',
        'Millennium'
    ],

    // The year adjustment used for supra-year intervals.
    YEAR_FRACTIONS : {
      Millenium     : 1000,
      Quincentenary : 500,
      Century       : 100,
      HalfCentury   : 50,
      Decade        : 10,
      Lustrum       : 5
    },

    // A test to find the appropriate range of intervals, for example if a range of
    // timestamps only spans hours this will return true when called with `"Hours"`.
    isAtLeastA : function(interval) {
      return ((this.max - this.min) > this.INTERVALS[interval]);
    },

    // Find the maximum interval we should use based on the estimates in `INTERVALS`.
    computeMaxInterval : function() {
      for (var i = 0; i < this.INTERVAL_ORDER.length; i++) {
        if (!this.isAtLeastA(this.INTERVAL_ORDER[i])) break;
      }
      return i - 1;
    },

    // Return the calculated `maxInterval`.
    getMaxInterval : function() {
      return this.INTERVALS[this.INTERVAL_ORDER[this.idx]];
    },

    // Floor the year to a given epoch.
    getYearFloor : function(date, intvl){
      var fudge = this.YEAR_FRACTIONS[intvl] || 1;
      return (date.getFullYear() / fudge | 0) * fudge;
    },

    // Return a date with the year set to the next interval in a given epoch.
    getYearCeil : function(date, intvl){
      if(this.YEAR_FRACTIONS[intvl]) return this.getYearFloor(date, intvl) + this.YEAR_FRACTIONS[intvl];
      return date.getFullYear();
    },

    // Return a Date object rounded down to the previous Sunday, a.k.a. the first day of the week.
    getWeekFloor: function(date) {
      thisDate = new Date(date.getFullYear(), date.getMonth(), date.getDate());
      thisDate.setDate(date.getDate() - date.getDay());
      return thisDate;
    },

    // Return a Date object rounded up to the next Sunday, a.k.a. the start of the next week.
    getWeekCeil: function(date) {
      thisDate = new Date(date.getFullYear(), date.getMonth(), date.getDate());
      thisDate.setDate(thisDate.getDate() + (7 - date.getDay()));
      return thisDate;
    },

    // Return the half of the hour this date belongs to. Anything before 30 min.
    // past the hour comes back as zero. Anything after comes back as 30.
    getHalfHour: function(date) {
      return date.getMinutes() > 30 ? 30 : 0;
    },

    // Return the quarter of the hour this date belongs to. Anything before 15 min.
    // past the hour comes back as zero; 15-30 comes back as 15; 30-45 as 30;
    // 45-60 as 45.
    getQuarterHour: function(date) {
      var minutes = date.getMinutes();
      if (minutes < 15) return 0;
      if (minutes < 30) return 15;
      if (minutes < 45) return 30;
      return 45;
    },

    // Zero out a date from the current interval down to seconds.
    floor : function(ts){
      var date  = new Date(ts);
      var intvl = this.INTERVAL_ORDER[this.idx];
      var idx   = this.idx > _.indexOf(this.INTERVAL_ORDER,'FullYear') ?
                  _.indexOf(this.INTERVAL_ORDER,'FullYear') :
                  idx;

      // Zero the special extensions, and adjust as idx necessary.
      date.setFullYear(this.getYearFloor(date, intvl));
      switch(intvl){
        case 'Week':
          date.setDate(this.getWeekFloor(date).getDate());
          idx = _.indexOf(this.INTERVAL_ORDER, 'Week');
        case 'HalfHour':
          date.setMinutes(this.getHalfHour(date));
        case 'QuarterHour':
          date.setMinutes(this.getQuarterHour(date));
      }

      // Zero out the rest
      while(idx--){
        intvl = this.INTERVAL_ORDER[idx];
        if (!(_.include(['Week', 'HalfHour', 'QuarterHour'].concat(_.keys(this.YEAR_FRACTIONS)), intvl)))
          date["set" + intvl](intvl === "Date" ? 1 : 0);
      }

      return date.getTime();
    },

    // Find the next date based on the past in timestamp.
    ceil : function(ts){
      var date = new Date(this.floor(ts));
      var intvl = this.INTERVAL_ORDER[this.idx];

      date.setFullYear(this.getYearCeil(date, intvl));
      switch(intvl){
        case 'Week':
          date.setTime(this.getWeekCeil(date).getTime());
          break;
        case 'HalfHour':
          date.setMinutes(this.getHalfHour(date) + 30);
          break;
        case 'QuarterHour':
          date.setMinutes(this.getQuarterHour(date) + 15);
          break;
        default:
          if (!(_.include(['Week', 'HalfHour', 'QuarterHour'].concat(_.keys(this.YEAR_FRACTIONS)), intvl)))
            date["set" + intvl](date["get" + intvl]() + 1);
      }
      return date.getTime();
    },

    // The actual difference in timespans accounting for time oddities like
    // different length months and leap years.
    span : function(ts){
      return this.ceil(ts) - this.floor(ts);
    },

    // Calculate and return a list of human formatted strings and raw timestamps.
    getRanges : function() {
      if (this.intervals) return this.intervals;
      this.intervals = [];
      for (var i = this.floor(this.min); i <= this.ceil(this.max); i += this.span(i)) {
        this.intervals.push({
          human     : Intervals.dateStr(i, this.maxInterval),
          timestamp : i
        });
      }
      return this.intervals;
    }
  };

  // Handy dandy function to bind a listener on multiple events. For example,
  // `Bar` and `CardContainer` are bound like so on "move" and "zoom":
  //
  //      sync(this.bar, this.cardCont, "move", "zoom");
  //
  var sync = function(origin, listener){
    var events = Array.prototype.slice.call(arguments, 2);
    _.each(events, function(ev){
      origin.bind(ev, function(){ listener[ev].apply(listener, arguments); });
    });
  };

  // Simple function to strip suffixes like `"px"` and return a clean integer for
  // use.
  var cleanNumber = function(str){
    return parseInt(str.replace(/^[^+\-\d]?([+\-]?\d+)?.*$/, "$1"), 10);
  };

  // Zero pad a number less than 10 and return a 2 digit value.
  var padNumber = function(number) {
    return (number < 10 ? '0' : '') + number;
  };

  // A quick and dirty hash manager for setting and getting values from
  // `window.location.hash`
  var hashStrip = /^#*/;
  var history = {
    get : function(){
      return window.location.hash.replace(hashStrip, "");
    },

    set : function(url){
      window.location.hash = url;
    }
  };

  // Every new `Series` gets new color. If there are too many series
  // the remaining series will be a simple gray.

  // These colors can be styled like such in
  // timeline-setter.css, where the numbers 1-9 cycle through in that order:
  //
  //      .TS-notch_color_1,.TS-series_legend_swatch_1 {
  //        background: #065718 !important;
  //      }
  //      .TS-css_arrow_color_1 {
  //        border-bottom-color:#065718 !important;
  //      }
  //      .TS-item_color_1 {
  //       border-top:1px solid #065718 !important;
  //      }
  //
  // The default color will fall through to what is styled with
  // `.TS-foo_color_default`
  var curColor = 1;
  var color = function(){
    var chosen;
    if (curColor < 10) {
      chosen = curColor;
      curColor += 1;
    } else {
      chosen = "default";
    }
    return chosen;
  };



  // Models
  // ------

  // Initialize a Timeline object in a container element specified
  // in the config object.
  var Timeline = TimelineSetter.Timeline = function(data, config) {
    _.bindAll(this, 'render', 'setCurrentTimeline');
    this.data = data.sort(function(a, b){ return a.timestamp - b.timestamp; });
    this.bySid    = {};
    this.cards    = [];
    this.series   = [];
    this.config   = config;
    this.config.container = this.config.container || "#timeline";

    // Override default date formats
    // by writing a `formatter` function that returns
    // an object containing all the formats
    // you'd like to override. Pass in `d`
    // which is a date object, and `defaults`, which
    // are the formatters we override.
    //
    //      formatter : function(d, defaults) {
    //        defaults.months = ['enero', 'febrero', 'marzo',
    //                          'abril', 'mayo', 'junio', 'julio',
    //                          'agosto', 'septiembre', 'octubre',
    //                          'noviembre', 'diciembre'];
    //        return defaults;
    //      }
    Intervals.formatter = this.config.formatter || function(d, defaults) { return defaults; };
  };
  observable(Timeline.prototype);

  Timeline.prototype = _.extend(Timeline.prototype, {
    // The main kickoff point for rendering the timeline. The `Timeline` constructor
    // takes a JSON array of card representations and then builds series, calculates
    // intervals `sync`s the `Bar` and `CardContainer` objects.
    render : function() {
      var that = this;

      // create `this.$` from queryable mixin.
      queryable(this, this.config.container);

      // Stick the barebones HTML structure in the dom,
      // so we can play with it.
      $(this.config.container).html(JST.timeline());

      this.bounds   = new Bounds();
      this.bar      = new Bar(this);
      this.cardCont = new CardScroller(this);
      this.createSeries(this.data);
      var range = new Intervals(this.bounds, this.config.interval);
      this.intervals = range.getRanges();
      this.bounds.extend(this.bounds.min - range.getMaxInterval() / 2);
      this.bounds.extend(this.bounds.max + range.getMaxInterval() / 2);
      this.bar.render();
      sync(this.bar, this.cardCont, "move", "zoom");
      this.trigger('render');

      new Zoom("in", this);
      new Zoom("out", this);
      this.chooseNext = new Chooser("next", this);
      this.choosePrev = new Chooser("prev", this);
      if (!this.$(".TS-card_active").is("*")) this.chooseNext.click();

      // Bind a click handler to this timeline container
      // that sets it as as the global current timeline
      // for key presses.
      $(this.config.container).bind('click', this.setCurrentTimeline);

      this.trigger('load');
    },

    // Set a global with the current timeline, mostly for key presses.
    setCurrentTimeline : function() {
      TimelineSetter.currentTimeline = this;
    },

    // Loop through the JSON and add each element to a series.
    createSeries : function(series){
      for(var i = 0; i < series.length; i++)
        this.add(series[i]);
    },

    // If a particular element in the JSON array mentions a series that's not
    // in the `bySid` object add it. Then add a card to the `Series` and extend
    // the global `bounds`.
    add : function(card){
      if (!(card.series in this.bySid)){
        this.bySid[card.series] = new Series(card, this);
        this.series.push(this.bySid[card.series]);
      }
      var series = this.bySid[card.series];
      var crd = series.add(card);

      this.bounds.extend(series.max());
      this.bounds.extend(series.min());

      this.trigger('cardAdd', card, crd);
    }
  });




  // Views
  // -----

  // The main interactive element in the timeline is `.TS-notchbar`. Behind the
  // scenes `Bar` handles the moving and zooming behaviours through the `draggable`
  // and `wheel` plugins.
  var Bar = function(timeline) {
    var that = this;
    this.timeline = timeline;

    this.el = this.timeline.$(".TS-notchbar");
    this.el.css({ "left": 0 });
    draggable(this);
    wheel(this);
    _.bindAll(this, "moving", "doZoom");
    this.el.bind("dragging scrolled", this.moving);
    this.el.bind("doZoom", this.doZoom);
    this.el.bind("dblclick doubletap", function(e){
      e.preventDefault();
      that.timeline.$(".TS-zoom_in").click();
    });
  };
  observable(Bar.prototype);
  transformable(Bar.prototype);

  Bar.prototype = _.extend(Bar.prototype, {
    // Every time the `Bar` is moved, it calculates whether the proposed movement
    // will move the `.TS-notchbar` off of its parent. If so, it recaculates
    // `deltaX` to be a more correct value.
    moving : function(e){
      var parent  = this.el.parent();
      var pOffset = parent.offset().left;
      var offset  = this.el.offset().left;
      var width   = this.el.width();
      if (_.isUndefined(e.deltaX)) e.deltaX = 0;

      if (offset + width + e.deltaX < pOffset + parent.width())
        e.deltaX = (pOffset + parent.width()) - (offset + width);
      if (offset + e.deltaX > pOffset)
        e.deltaX = pOffset - offset;

      this.trigger("move", e);
      this.timeline.trigger("move", e); // for API
      this.move("move", e);
    },

    // As the timeline zooms, the `Bar` tries to keep the current notch (i.e.
    // `.TS-notch_active`) as close to its original position as possible.
    // There's a slight bug here because the timeline zooms and then moves the
    // bar to correct for this behaviour, and in future versions we'll fix this.
    doZoom : function(e, width){
      var that = this;
      var notch = this.timeline.$(".TS-notch_active");
      var getCur = function() {
        return notch.length > 0 ? notch.position().left : 0;
      };
      var curr = getCur();

      this.el.animate({"width": width + "%"}, {
        step: function(current, fx) {
          var e = $.Event("dragging");
          var delta = curr - getCur();
          e.deltaX = delta;
          that.moving(e);
          curr = getCur();
          e = $.Event("zoom");
          e.width = current + "%";
          that.trigger("zoom", e);
        }
      });
    },

    // When asked to render the bar places the appropriate timestamp notches
    // inside `.TS-notchbar`.
    render : function(){
      var intervals = this.timeline.intervals;
      var bounds    = this.timeline.bounds;

      for (var i = 0; i < intervals.length; i++) {
        var html = JST.year_notch({'timestamp' : intervals[i].timestamp, 'human' : intervals[i].human });
        this.el.append($(html).css("left", bounds.project(intervals[i].timestamp, 100) + "%"));
      }
    }
  });


  // The `CardScroller` mirrors the moving and zooming of the `Bar` and is the
  // canvas where individual cards are rendered.
  var CardScroller = function(timeline){
    this.el = timeline.$(".TS-card_scroller_inner");
  };
  observable(CardScroller.prototype);
  transformable(CardScroller.prototype);


  // Each `Series` picks a unique color and keeps an array of `Cards`.
  var Series = function(series, timeline) {
    this.timeline = timeline;
    this.name     = series.series;
    this.color    = this.name.length > 0 ? color() : "default";
    this.cards    = [];
    _.bindAll(this, "render", "showNotches", "hideNotches");
    this.timeline.bind("render", this.render);
  };
  observable(Series.prototype);

  Series.prototype = _.extend(Series.prototype, {
    // Create and add a particular card to the cards array.
    add : function(card){
      var crd = new Card(card, this);
      this.cards.push(crd);
      return crd;
    },

    // The comparing function for `max` and `min`.
    _comparator : function(crd){
      return crd.timestamp;
    },

    // Inactivate this series legend item and trigger a `hideNotch` event.
    hideNotches : function(e){
      if(e) e.preventDefault();
      this.el.addClass("TS-series_legend_item_inactive");
      this.trigger("hideNotch");
    },

    // Activate the legend item and trigger the `showNotch` event.
    showNotches : function(e){
      if(e) e.preventDefault();
      this.el.removeClass("TS-series_legend_item_inactive");
      this.trigger("showNotch");
    },

    // Create and append the label to `.TS-series_nav_container` and bind up
    // `hideNotches` and `showNotches`.
    render : function(e){
      if (this.name.length === 0) return;
      this.el = $(JST.series_legend(this));
      this.timeline.$(".TS-series_nav_container").append(this.el);
      var counter = 0, that = this;
      this.el.on("click", function(){
        counter++;
        if(counter % 2)
          that.hideNotches();
        else
          that.showNotches();
      });
    }
  });

  // Proxy to underscore for `min` and `max`.
  _(["min", "max"]).each(function(key){
    Series.prototype[key] = function() {
      return _[key].call(_, this.cards, this._comparator).get("timestamp");
    };
  });


  // Every `Card` handles a notch div which is immediately appended to the `Bar`
  // and a `.TS-card_container` which is lazily rendered.
  var Card = function(card, series) {
    this.series = series;
    this.timeline = this.series.timeline;
    card = _.clone(card);
    this.timestamp = card.timestamp;
    this.attributes = card;
    this.attributes.topcolor = series.color;
    _.bindAll(this, "render", "activate", "flip", "setPermalink", "toggleNotch");
    this.series.bind("hideNotch", this.toggleNotch);
    this.series.bind("showNotch", this.toggleNotch);
    this.timeline.bind("render", this.render);
    this.timeline.bar.bind("move", this.flip);
    this.id = [
      this.get('timestamp'),
      this.get('description').split(/ /)[0].replace(/[^a-zA-Z\-]/g,"")
    ].join("-");
    this.timeline.cards.push(this);
    this.template = window.JST.card;
  };

  Card.prototype = _.extend(Card.prototype, {
    // Get a particular attribute by key.
    get : function(key){
      return this.attributes[key];
    },

    // When each `Card` is rendered via a render event, it appends a notch to the
    // `Bar` and binds a click handler so it can be activated. if the `Card`'s id
    // is currently selected via `window.location.hash` it's activated.
    render : function(){
      this.offset = this.timeline.bounds.project(this.timestamp, 100);
      var html = JST.notch(this.attributes);
      this.notch = $(html).css({"left": this.offset + "%"});
      this.timeline.$(".TS-notchbar").append(this.notch);
      this.notch.click(this.activate);
      if (history.get() === this.id) this.activate();
    },

    // As the `Bar` moves the current card checks to see if it's outside the viewport,
    // if it is the card is flipped so as to be visible for the longest period
    // of time. The magic number here (7) is half the width of the css arrow.
    flip : function() {
      if (!this.el || !this.el.is(":visible")) return;
      var rightEdge   = this.$(".TS-item").offset().left + this.$(".TS-item").width();
      var tRightEdge  = this.timeline.$(".timeline_setter").offset().left + this.timeline.$(".timeline_setter").width();
      var margin      = this.el.css("margin-left") === this.originalMargin;
      var flippable   = this.$(".TS-item").width() < this.timeline.$(".timeline_setter").width() / 2;
      var offTimeline = (this.el.offset().left - this.el.parent().offset().left) - this.$(".TS-item").width() < 0;

      // If the card's right edge is more than the timeline's right edge and
      // it's never been flipped before and it won't go off the timeline when
      // flipped. We'll flip it.
      if (tRightEdge - rightEdge < 0 && margin && !offTimeline) {
        this.el.css({"margin-left": -(this.$(".TS-item").width() + 7)});
        this.$(".TS-css_arrow").css({"left" : this.$(".TS-item").width()});
        // Otherwise, if the card is off the left side of the timeline and we have
        // flipped it before and the card's width is less than half of the width
        // of the whole timeline, we'll flip it to the default position.
      } else if (this.el.offset().left - this.timeline.$(".timeline_setter").offset().left < 0 && !margin && flippable) {
        this.el.css({"margin-left": this.originalMargin});
        this.$(".TS-css_arrow").css({"left": 0});
      }
    },

    // The first time a card is activated it renders its `template` and appends
    // its element to the `Bar`. After doing so it sets the width if `.TS-item_label`
    // and moves the `Bar` if its element outside the visible portion of the
    // timeline.
    activate : function(e){
      var that = this;
      this.hideActiveCard();
      if (!this.el) {
        this.el = $(this.template({card: this}));

        // create a `this.$` scoped to its card.
        queryable(this, this.el);

        this.el.css({"left": this.offset + "%"});
        this.timeline.$(".TS-card_scroller_inner").append(this.el);
        this.originalMargin = this.el.css("margin-left");
        this.el.delegate(".TS-permalink", "click", this.setPermalink);
        // Reactivate if there are images in the html so we can recalculate
        // widths and position accordingly.
        this.timeline.$("img").load(this.activate);
      }

      this.el.show().addClass(("TS-card_active"));
      this.notch.addClass("TS-notch_active");
      this.setWidth();

      // In the case that the card is outside the bounds the wrong way when
      // it's flipped, we'll take care of it here before we move the actual
      // card.
      this.flip();
      this.move();
      this.series.timeline.trigger("cardActivate", this.attributes);
    },

    // For Internet Explorer each card sets the width of` .TS-item_label` to
    // the maximum width of the card's children, or if that is less than the
    // `.TS-item_year` element's width, `.TS-item_label` gets `.TS-item_year`s
    // width. Which is a funny way of saying, if you'd like to set the width of
    // the card as a whole, fiddle with `.TS-item_year`s width.
    setWidth : function(){
      var that = this;
      var max = _.max(_.toArray(this.$(".TS-item_user_html").children()), function(el){ return that.$(el).width(); });
      if (this.$(max).width() > this.$(".TS-item_year").width()) {
        this.$(".TS-item_label").css("width", this.$(max).width());
      } else {
        this.$(".TS-item_label").css("width", this.$(".TS-item_year").width());
      }
    },

    // Move the `Bar` if the card is outside the visible region on activation.
    move : function() {
      var e = $.Event('moving');
      var offset  = this.$(".TS-item").offset();
      var toffset = this.timeline.$(".timeline_setter").offset();
      if (offset.left < toffset.left) {
        e.deltaX = toffset.left - offset.left + cleanNumber(this.$(".TS-item").css("padding-left"));
        this.timeline.bar.moving(e);
      } else if (offset.left + this.$(".TS-item").outerWidth() > toffset.left + this.timeline.$(".timeline_setter").width()) {
        e.deltaX = toffset.left + this.timeline.$(".timeline_setter").width() - (offset.left + this.$(".TS-item").outerWidth());
        this.timeline.bar.moving(e);
      }
    },

    // The click handler to set the current hash to the `Card`'s id.
    setPermalink : function() {
      history.set(this.id);
    },

    // Globally hide any cards with `TS-card_active`.
    hideActiveCard : function() {
      this.timeline.$(".TS-card_active").removeClass("TS-card_active").hide();
      this.timeline.$(".TS-notch_active").removeClass("TS-notch_active");
    },

    // An event listener to toggle this notch on and off via `Series`.
    toggleNotch : function(e){
      switch (e) {
        case "hideNotch":
          this.notch.hide().removeClass("TS-notch_active").addClass("TS-series_inactive");
          if (this.el) this.el.hide();
          return;
        case "showNotch":
          this.notch.removeClass("TS-series_inactive").show();
      }
    }

  });


  // Simple inheritance helper for `Controls`.
  var ctor = function(){};
  var inherits = function(child, parent){
    ctor.prototype  = parent.prototype;
    child.prototype = new ctor();
    child.prototype.constructor = child;
  };

  // Controls
  // --------

  // Each control is basically a callback wrapper for a given DOM element.
  var Control = function(direction, timeline){
    this.timeline = timeline;
    this.direction = direction;
    this.el = this.timeline.$(this.prefix + direction);
    var that = this;
    this.el.bind('click', function(e) { e.preventDefault(); that.click(e);});
  };

  // Each `Zoom` control adjusts the `curZoom` when clicked.
  var curZoom = 100;
  var Zoom = function(direction, timeline) {
    Control.apply(this, arguments);
  };
  inherits(Zoom, Control);

  Zoom.prototype = _.extend(Zoom.prototype, {
    prefix : ".TS-zoom_",

    // Adjust the `curZoom` up or down by 100 and trigger a `doZoom` event on
    // `.TS-notchbar`
    click : function() {
      curZoom += (this.direction === "in" ? +100 : -100);
      if (curZoom >= 100) {
        this.timeline.$(".TS-notchbar").trigger('doZoom', [curZoom]);
      } else {
        curZoom = 100;
      }
    }
  });


  // Each `Chooser` activates the next or previous notch.
  var Chooser = function(direction, timeline) {
    Control.apply(this, arguments);
    this.notches = this.timeline.$(".TS-notch");
  };
  inherits(Chooser, Control);

  Chooser.prototype = _.extend(Control.prototype, {
    prefix: ".TS-choose_",

    // Figure out which notch to activate and do so by triggering a click on
    // that notch.
    click: function(e){
      var el;
      var notches    = this.notches.not(".TS-series_inactive");
      var curCardIdx = notches.index(this.timeline.$(".TS-notch_active"));
      var numOfCards = notches.length;
      if (this.direction === "next") {
        el = (curCardIdx < numOfCards ? notches.eq(curCardIdx + 1) : false);
      } else {
        el = (curCardIdx > 0 ? notches.eq(curCardIdx - 1) : false);
      }
      if (!el) return;
      el.trigger("click");
    }
  });

  // JS API
  // ------

  // The TimelineSetter JS API allows you to listen to certain
  // timeline events, and activate cards programmatically.
  // To take advantage of it, assign the timeline boot function to a variable
  // like so:
  //
  //     var currentTimeline = TimelineSetter.Timeline.boot(
  //       [data], {config}
  //     );
  //
  // then call methods on the `currentTimeline.api` object
  //
  //     currentTimeline.api.onLoad(function() {
  //       console.log("I'm ready");
  //     });
  //
  TimelineSetter.Api = function(timeline) {
    this.timeline = timeline;
  };

  TimelineSetter.Api.prototype = _.extend(TimelineSetter.Api.prototype, {
    // Register a callback for when the timeline is loaded
    onLoad : function(cb) {
      this.timeline.bind('load', cb);
    },

    // Register a callback for when a card is added to the timeline
    // Callback has access to the event name and the card object
    onCardAdd : function(cb) {
      this.timeline.bind('cardAdd', cb);
    },

    // Register a callback for when a card is activated.
    // Callback has access to the event name and the card object
    onCardActivate : function(cb) {
      this.timeline.bind('cardActivate', cb);
    },

    // Register a callback for when the bar is moved or zoomed.
    // Be careful with this one: Bar move events can be fast
    // and furious, especially with scroll wheels in Safari.
    onBarMove : function(cb) {
      // Bind a 'move' event to the timeline, because
      // at this point, `timeline.bar` isn't available yet.
      // To get around this, we'll trigger the bar's
      // timeline's move event when the bar is moved.
      this.timeline.bind('move', cb);
    },

    // Show the card matching a given timestamp
    // Right now, timelines only support one card per timestamp
    activateCard : function(timestamp) {
      _(this.timeline.cards).detect(function(card) { return card.timestamp === timestamp; }).activate();
    }
  });

  // Global TS keydown function to bind key events to the
  // current global currentTimeline.
  TimelineSetter.bindKeydowns = function() {
    $(document).bind('keydown', function(e) {
      if (e.keyCode === 39) {
         TimelineSetter.currentTimeline.chooseNext.click();
      } else if (e.keyCode === 37) {
        TimelineSetter.currentTimeline.choosePrev.click();
      } else {
        return;
      }
    });
  };


  // Finally, let's create the whole timeline. Boot is exposed globally via
  // `TimelineSetter.Timeline.boot()` which takes the JSON generated by
  // the timeline-setter binary as its first argument, and a config hash as its second.
  // The config hash looks for a container element, an interval for interval notches
  // and a formatter function for dates. All of these are optional.
  //
  // We also initialize a new API object for each timeline, accessible via the
  // timeline variable's `api` method (e.g. `currentTimeline.api`) and look for
  // how many timelines are globally on the page for keydown purposes. We'll only
  // bind keydowns globally if there's only one timeline on the page.
  Timeline.boot = function(data, config) {
    var timeline = TimelineSetter.timeline = new Timeline(data, config || {});
    var api      = new TimelineSetter.Api(timeline);

    if (!TimelineSetter.pageTimelines) {
      TimelineSetter.currentTimeline = timeline;
      TimelineSetter.bindKeydowns();
    }

    TimelineSetter.pageTimelines = TimelineSetter.pageTimelines ? TimelineSetter.pageTimelines += 1 : 1;

    $(timeline.render);

    return {
      timeline : timeline,
      api      : api
    };
  };

})(jQuery);
(function(){window.JST=window.JST||{};var a=function(c){var b=new Function("obj","var __p=[],print=function(){__p.push.apply(__p,arguments);};with(obj||{}){__p.push('"+c.replace(/\\/g,"\\\\").replace(/'/g,"\\'").replace(/<%=([\s\S]+?)%>/g,function(d,e){return"',"+e.replace(/\\'/g,"'")+",'"}).replace(/<%([\s\S]+?)%>/g,function(d,e){return"');"+e.replace(/\\'/g,"'").replace(/[\r\n\t]/g," ")+"__p.push('"}).replace(/\r/g,"\\r").replace(/\n/g,"\\n").replace(/\t/g,"\\t")+"');}return __p.join('');");return b};window.JST.card=a('<div class="TS-card_container TS-card_container_<%= (card.get("series") || "").replace(/W/g, "") %>">\n<div class="TS-css_arrow TS-css_arrow_up TS-css_arrow_color_<%= card.get("topcolor") %>"></div>\n  <div class="TS-item TS-item_color_<%= card.get("topcolor") %>" data-timestamp="<%= card.get("timestamp") %>">\n    <div class="TS-item_label">\n      <% if (!_.isEmpty(card.get("html"))){ %>\n        <div class="TS-item_user_html">\n          <%= card.get("html") %>\n        </div>\n      <% } %>\n      <%= card.get("description") %>\n    </div>\n      <% if (!_.isEmpty(card.get("link"))){ %>\n          <a class="TS-read_btn" target="_blank" href="<%= card.get("link") %>">Read More</a>\n      <% } %>\n\n    <div class="TS-item_year">\n      <span class="TS-item_year_text"><%= (card.get("display_date") || "").length > 0 ? card.get("display_date") : card.get("date") %></span>\n      <div class="TS-permalink">&#8734;</div>\n    </div>\n  </div>\n</div>');window.JST.notch=a('<div class="TS-notch TS-notch_<%= timestamp %> TS-notch_<%= series.replace(/W/g, "") %> TS-notch_color_<%= topcolor %>"></div>\n');window.JST.series_legend=a('<div class="TS-series_legend_item TS-series_legend_item_<%= name.replace(/W/g, "") %>">\n  <span class="TS-series_legend_swatch TS-series_legend_swatch_<%= color %>">&nbsp;</span> <span class="TS-series_legend_text"><%= name %></span>\n</div>\n');window.JST.timeline=a('<div class="timeline_setter">\n  <div class="TS-top_matter_container">\n    <div class="TS-controls">\n      <div class="TS-zoom-container">\n        <a href="#" class="TS-zoom TS-zoom_in"><span class="TS-controls_inner_text TS-zoom_inner_text">+</span></a> \n        <a href="#" class="TS-zoom TS-zoom_out"><span class="TS-controls_inner_text TS-zoom_inner_text">-</span></a>\n      </div> \n      <div class="TS-choose-container">\n        <a href="#" class="TS-choose TS-choose_prev">&laquo;&nbsp;<span class="TS-controls_inner_text">Previous</span></a> \n        <a href="#" class="TS-choose TS-choose_next"><span class="TS-controls_inner_text">Next</span>&nbsp;&raquo;</a>\n      </div>\n    </div>\n    <div class="TS-series_nav_container"></div>\n  </div>\n\n  <div class="TS-notchbar_container">\n    <div class="TS-notchbar"></div>\n  </div>\n  <div class="TS-card_scroller">\n    <div class="TS-card_scroller_inner">\n    </div>\n  </div>\n</div>');window.JST.year_notch=a('<div class="TS-year_notch TS-year_notch_<%= timestamp %>">\n  <span class="TS-year_notch_year_text"><%= human %></span>\n</div>\n')})();