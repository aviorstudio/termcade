extends SceneTree

const PROTOCOL := 2
const MAX_REQUEST := 65536
var peer := StreamPeerTCP.new()
var started := false
var width := 144
var height := 40
var pixel_aspect := 0.5
var logical_size: Vector2i
var native_resolution := false
var input_bytes := PackedByteArray()
var pressed_codes: Array[int] = []

func _initialize() -> void:
	var args := OS.get_cmdline_user_args()
	if args.size() != 2:
		printerr("Termcade runtime requires its host connection.")
		quit(2)
		return
	var err := peer.connect_to_host("127.0.0.1", int(args[0]))
	if err != OK:
		quit(2)
		return
	while peer.get_status() == StreamPeerTCP.STATUS_CONNECTING:
		peer.poll()
		OS.delay_usec(1000)
	peer.set_no_delay(true)
	logical_size = Vector2i(ProjectSettings.get_setting("display/window/size/viewport_width", 0), ProjectSettings.get_setting("display/window/size/viewport_height", 0))
	if logical_size.x <= 0 or logical_size.y <= 0:
		logical_size = root.size
	native_resolution = OS.get_environment("TERMCADE_GODOT_FULL_RES") == "1"
	# Keep authored coordinates and layout while rendering only the pixels
	# the terminal can show. Native resolution remains available for games
	# whose scripts or shaders depend on the original render target size.
	if not native_resolution:
		root.content_scale_size = logical_size
		root.content_scale_mode = Window.CONTENT_SCALE_MODE_CANVAS_ITEMS
		root.content_scale_aspect = Window.CONTENT_SCALE_ASPECT_KEEP
	root.disable_3d = false
	RenderingServer.render_loop_enabled = false
	_send({"protocol": PROTOCOL, "token": args[1], "title": ProjectSettings.get_setting("application/config/name", "Godot game")})

func _process(_delta: float) -> bool:
	# Host-driven fixed-FPS engine iterations. While the arcade is paused or
	# waiting on input, Godot cannot run ahead and accumulate invisible frames.
	while peer.get_status() == StreamPeerTCP.STATUS_CONNECTED:
		peer.poll()
		var available := peer.get_available_bytes()
		if available > 0:
			var result := peer.get_data(mini(available, MAX_REQUEST))
			if result[0] != OK:
				return true
			input_bytes.append_array(result[1])
			if input_bytes.size() > MAX_REQUEST:
				return true
			var newline := input_bytes.find(10)
			if newline >= 0:
				var request = JSON.parse_string(input_bytes.slice(0, newline).get_string_from_utf8())
				input_bytes = input_bytes.slice(newline + 1)
				if not request is Dictionary:
					return true
				return _request(request)
		OS.delay_usec(1000)
	return true

func _request(request: Dictionary) -> bool:
	if request.get("op") == "close":
		return true
	width = int(request.get("width", width))
	height = int(request.get("height", height))
	pixel_aspect = float(request.get("pixel_aspect", pixel_aspect))
	if width < 1 or width > 600 or height < 1 or height > 360 or pixel_aspect <= 0 or pixel_aspect > 2:
		_send({"error": "invalid framebuffer dimensions"})
		return true
	if not native_resolution:
		var scale := minf(1.0, minf(float(width) / logical_size.x, float(height) / pixel_aspect / logical_size.y))
		var render_size := Vector2i(maxi(1, roundi(logical_size.x * scale)), maxi(1, roundi(logical_size.y * scale)))
		if root.size != render_size:
			root.size = render_size
	if request.get("op") == "reset":
		for code in pressed_codes:
			var release := InputEventKey.new()
			release.keycode = code
			release.physical_keycode = code
			Input.parse_input_event(release)
		pressed_codes.clear()
		Input.flush_buffered_events()
		if is_instance_valid(current_scene):
			current_scene.free()
		var packed := load(ProjectSettings.get_setting("application/run/main_scene", "")) as PackedScene
		if packed == null:
			_send({"error": "cannot load main scene"})
			return true
		paused = false
		var scene := packed.instantiate()
		root.add_child(scene)
		current_scene = scene
		started = true
	elif request.get("op") != "step" or not started:
		_send({"error": "expected reset or step"})
		return true
	for key in request.get("keys", []):
		var event := InputEventKey.new()
		event.keycode = int(key.code)
		event.physical_keycode = event.keycode
		event.pressed = bool(key.down)
		event.echo = event.pressed and pressed_codes.has(event.keycode)
		if event.pressed and not pressed_codes.has(event.keycode):
			pressed_codes.append(event.keycode)
		elif not event.pressed:
			pressed_codes.erase(event.keycode)
		Input.parse_input_event(event)
	Input.flush_buffered_events()
	_send_frame.call_deferred()
	return false

func _send_frame() -> void:
	# Godot renders every node, including cameras, UI, shaders and 3D. The
	# private display never appears on the desktop; no node emulation is used.
	var render_start := Time.get_ticks_usec()
	RenderingServer.force_draw(false, 1.0 / 60.0)
	var read_start := Time.get_ticks_usec()
	var frame := root.get_texture().get_image()
	var resize_start := Time.get_ticks_usec()
	var render_size := frame.get_size()
	if frame == null or frame.is_empty():
		_send({"error": "Godot did not produce a rendered viewport"})
		quit(2)
		return
	# Character cells are taller than they are wide. Fit the game's aspect
	# ratio into the available terminal space instead of stretching its UI.
	var scale := minf(width * pixel_aspect / frame.get_width(), float(height) / frame.get_height())
	var fitted := Vector2i(maxi(1, roundi(frame.get_width() * scale / pixel_aspect)), maxi(1, roundi(frame.get_height() * scale)))
	fitted.x = mini(fitted.x, width)
	fitted.y = mini(fitted.y, height)
	frame.resize(fitted.x, fitted.y, Image.INTERPOLATE_BILINEAR)
	frame.convert(Image.FORMAT_RGB8)
	var output := Image.create(width, height, false, Image.FORMAT_RGB8)
	output.fill(Color.BLACK)
	output.blit_rect(frame, Rect2i(Vector2i.ZERO, fitted), Vector2i((width - fitted.x) / 2, (height - fitted.y) / 2))
	frame = output
	_send({"width": width, "height": height, "pixels": Marshalls.raw_to_base64(frame.get_data()), "render_width": render_size.x, "render_height": render_size.y, "timings": {"render_us": read_start - render_start, "readback_us": resize_start - read_start, "resize_us": Time.get_ticks_usec() - resize_start}})

func _send(value: Dictionary) -> void:
	peer.put_data((JSON.stringify(value) + "\n").to_utf8_buffer())
